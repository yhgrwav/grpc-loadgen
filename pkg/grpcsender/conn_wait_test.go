// Copyright 2026 yhgrwav
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package grpcsender

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// connWaitSlack is how far above the held wait ConnWait may read on a stand:
// the release, the handshake and the pick after it. Provisional until CI runs
// name the number.
const connWaitSlack = 25 * time.Millisecond

// feed plays events into a call as grpc-go would, with the handler's clock at
// each event's moment.
type feed struct {
	t0   time.Time
	call *callStats
	ctx  context.Context
	now  time.Time
}

func newFeed(invokedAfter time.Duration) *feed {
	f := &feed{t0: time.Unix(1_000_000, 0), call: &callStats{}}
	f.ctx = context.WithValue(context.Background(), callKey{}, f.call)
	f.call.times.invokedAt = f.t0.Add(invokedAfter)

	return f
}

func (f *feed) at(ms int, rpc stats.RPCStats) {
	f.now = f.t0.Add(time.Duration(ms) * time.Millisecond)
	handler{clock: func() time.Time { return f.now }}.HandleRPC(f.ctx, rpc)
}

// begin opens an attempt; resolved says grpc-go waited for the resolver
// before it, which it reports in TagRPC, not in Begin.
func (f *feed) begin(ms int, resolved, retry bool) {
	f.now = f.t0.Add(time.Duration(ms) * time.Millisecond)
	h := handler{clock: func() time.Time { return f.now }}
	ctx := h.TagRPC(f.ctx, &stats.RPCTagInfo{NameResolutionDelay: resolved})
	h.HandleRPC(ctx, &stats.Begin{Client: true, BeginTime: f.now, IsTransparentRetryAttempt: retry})
}

// Ground: boundary — grpc-go v1.84.0 emits DelayedPickComplete only when the
// pick blocked (picker_wrapper.go:150, stream.go:627), in the call's goroutine,
// and Begin once per attempt with its own BeginTime (stream.go:563). Name
// resolution waits before the first Begin (stream.go:338) and is reported in
// RPCTagInfo.NameResolutionDelay (stream.go:566).
func TestHandleRPC_ConnWaitSumsEachAttemptsWait(t *testing.T) {
	cases := []struct {
		name string
		play func(f *feed)
		want time.Duration
	}{
		{"no wait, no event", func(f *feed) {
			f.begin(0, false, false)
			f.at(1, &stats.OutPayload{SentTime: f.t0.Add(time.Millisecond)})
		}, 0},
		{"one blocked pick", func(f *feed) {
			f.begin(0, false, false)
			f.at(50, &stats.DelayedPickComplete{})
		}, 50 * time.Millisecond},
		// A mutation counting from the first BeginTime reads 120, the last
		// attempt only reads 10, nothing reads 0.
		{"two attempts apart", func(f *feed) {
			f.begin(0, false, false)
			f.at(10, &stats.DelayedPickComplete{})
			f.begin(100, false, true)
			f.at(110, &stats.DelayedPickComplete{})
		}, 20 * time.Millisecond},
		// Resolution took 40 ms before Begin, then the pick waited 10: counted
		// from BeginTime it reads 10.
		{"resolver before the first Begin", func(f *feed) {
			f.begin(0, true, false)
			f.at(10, &stats.DelayedPickComplete{})
		}, 50 * time.Millisecond},
		{"resolver, then no blocked pick", func(f *feed) {
			f.begin(0, true, false)
		}, 40 * time.Millisecond},
		// First attempt waited 50 ms and lost its transport before sending; the
		// retry blocks until the deadline. Not sent, and the 50 ms stay.
		{"measured wait kept on a call that never went out", func(f *feed) {
			f.begin(0, false, false)
			f.at(50, &stats.DelayedPickComplete{})
			f.begin(51, false, true)
			f.at(300, &stats.End{EndTime: f.t0.Add(300 * time.Millisecond)})
		}, 50 * time.Millisecond},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			invoked := time.Duration(0)
			if c.name == "resolver before the first Begin" || c.name == "resolver, then no blocked pick" {
				invoked = -40 * time.Millisecond
			}
			f := newFeed(invoked)
			c.play(f)

			if got := f.call.read().connWait; got != c.want {
				t.Errorf("connWait = %v, want %v", got, c.want)
			}
		})
	}
}

// gatedTarget serves raw HTTP/2 and hangs up the first connection on drop;
// every later connection gets its SETTINGS only once gate is closed, so the
// client stays CONNECTING for as long as the test holds it.
type gatedTarget struct {
	sender  *Sender
	started chan time.Time
	gate    chan struct{}
	drop    func()
}

func newGatedTarget(t *testing.T) *gatedTarget {
	t.Helper()

	g := &gatedTarget{started: make(chan time.Time, 1), gate: make(chan struct{})}
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })

	var first net.Conn
	var mu sync.Mutex
	accepted := make(chan struct{})
	go func() {
		for n := 1; ; n++ {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			if n == 1 {
				mu.Lock()
				first = conn
				mu.Unlock()
				close(accepted)
				go serveRaw(conn, answerOK, false)

				continue
			}
			go func() {
				select {
				case <-g.gate:
				case <-t.Context().Done():
					_ = conn.Close()

					return
				}
				serveRaw(conn, answerOK, false)
			}()
		}
	}()

	g.sender = New(Options{Target: "passthrough:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithChainUnaryInterceptor(signalStart(g.started)),
	}})
	t.Cleanup(func() { _ = g.sender.Close() })
	if err := g.sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}
	<-accepted

	g.drop = func() {
		mu.Lock()
		_ = first.Close()
		mu.Unlock()
		// Sent before the client has seen the drop, the call would go to the
		// dead transport and never wait for a pick.
		for g.sender.conn.GetState() == connectivity.Ready {
			if !g.sender.conn.WaitForStateChange(bounded(t), connectivity.Ready) {
				t.Fatal("the client never saw the connection drop")
			}
		}
	}

	return g
}

func answerOK(fr *http2.Framer, stream uint32, _ int) { trailersOnly(fr, stream, codes.OK) }

// signalStart reports the moment a call enters grpc-go, after Send's own mark.
func signalStart(started chan<- time.Time) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		select {
		case started <- time.Now():
		default:
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// sendHeld sends one call, holds the connection for hold after the call has
// entered grpc-go, then lets it through.
func sendHeld(t *testing.T, s *Sender, started <-chan time.Time, release func(), hold time.Duration) engine.Outcome {
	t.Helper()

	type result struct {
		out engine.Outcome
		err error
	}
	done := make(chan result, 1)
	before := time.Now()
	go func() {
		out, err := s.Send(bounded(t), request(before))
		done <- result{out, err}
	}()

	select {
	case <-started:
	case <-bounded(t).Done():
		t.Fatal("the call never entered grpc-go")
	}
	time.Sleep(hold)
	release()

	r := <-done
	if r.err != nil {
		t.Fatalf("send: %v", r.err)
	}
	// Whatever the answer, the request must have gone out: an unsent call
	// never reported its last wait.
	if r.out.NotSent {
		t.Fatalf("not sent (%v): the stand never let the call through", r.out.Err)
	}
	// The wait for a connection and for a stream come one after the other,
	// both before the request goes out.
	if total := r.out.SentAt.Sub(before); r.out.ConnWait+r.out.StreamWait > total {
		t.Errorf("ConnWait %v + StreamWait %v > %v from start to sent", r.out.ConnWait, r.out.StreamWait, total)
	}

	return r.out
}

func checkHeldWait(t *testing.T, out engine.Outcome, hold time.Duration) {
	t.Helper()

	if out.ConnWait < hold || out.ConnWait > hold+connWaitSlack {
		t.Errorf("ConnWait = %v, want [%v, %v]", out.ConnWait, hold, hold+connWaitSlack)
	}
}

// Ground: stand with known behavior — the handshake is held for exactly hold
// after the call entered grpc-go, so the call waited at least that long.
func TestSend_ConnWaitCoversAHeldHandshake(t *testing.T) {
	for _, hold := range []time.Duration{50 * time.Millisecond, 4 * time.Millisecond} {
		t.Run(hold.String(), func(t *testing.T) {
			g := newGatedTarget(t)
			g.drop()

			out := sendHeld(t, g.sender, g.started, func() { close(g.gate) }, hold)
			checkHeldWait(t, out, hold)
		})
	}
}

// gatedResolver hands out the address at once on the first build and, after
// the channel went idle, only once gate is closed.
type gatedResolver struct {
	gate   chan struct{}
	builds atomic.Int32
}

func (r *gatedResolver) Scheme() string { return "gated" }

func (r *gatedResolver) Build(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	state := resolver.State{Addresses: []resolver.Address{{Addr: "bufnet"}}}
	if r.builds.Add(1) == 1 {
		_ = cc.UpdateState(state)

		return nopResolver{}, nil
	}
	go func() {
		<-r.gate
		_ = cc.UpdateState(state)
	}()

	return nopResolver{}, nil
}

type nopResolver struct{}

func (nopResolver) ResolveNow(resolver.ResolveNowOptions) {}
func (nopResolver) Close()                                {}

// Ground: boundary — grpc-go v1.84.0 waits for resolved addresses in
// newClientStream before the first Begin (stream.go:338); a wait counted from
// BeginTime misses it.
func TestSend_ConnWaitCoversNameResolution(t *testing.T) {
	const hold = 50 * time.Millisecond

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, &target{code: codes.OK})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	res := &gatedResolver{gate: make(chan struct{})}
	started := make(chan time.Time, 1)
	sender := New(Options{Target: "gated:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithResolvers(res),
		grpc.WithIdleTimeout(20 * time.Millisecond),
		grpc.WithChainUnaryInterceptor(signalStart(started)),
	}})
	t.Cleanup(func() { _ = sender.Close() })
	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Idle drops the resolver; the next call builds it again and waits.
	for sender.conn.GetState() != connectivity.Idle {
		if !sender.conn.WaitForStateChange(bounded(t), sender.conn.GetState()) {
			t.Fatal("the channel never went idle")
		}
	}

	out := sendHeld(t, sender, started, func() { close(res.gate) }, hold)
	if res.builds.Load() < 2 {
		t.Fatal("the resolver was not rebuilt: the call did not wait for it")
	}
	checkHeldWait(t, out, hold)
}

// Ground: contract — a call that waited only for a stream did not wait for a
// connection, and the two waits add up to no more than the time before sending.
func TestSend_StreamWaitIsNotConnWait(t *testing.T) {
	s := oneStreamTarget(t)

	out := sendWithin(t, s, 30*time.Millisecond)
	if !out.NotSent || out.NotSentOn != engine.BlockedOnStream {
		t.Fatalf("NotSent = %v on %v, want not sent on a stream", out.NotSent, out.NotSentOn)
	}
	if out.ConnWait != 0 {
		t.Errorf("ConnWait = %v on a ready connection, want 0", out.ConnWait)
	}
	if sent := sendWithin(t, dialTarget(t, &target{code: codes.OK}), time.Second); sent.ConnWait != 0 {
		t.Errorf("ConnWait = %v on a call that picked at once, want 0", sent.ConnWait)
	}
}
