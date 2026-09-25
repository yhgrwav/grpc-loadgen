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
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// How far above the held wait ConnWait may read: twice the largest excess in
// 300 runs each on Windows and in Linux (docker --cpus=2, -race).
//
// handshakeSlack covers the release, the rest of the handshake and the pick
// after it: max 2.4 ms. It is above the 4 ms hold, so that case checks only
// the lower bound: a short drop is not lost.
//
// resolveSlack covers a whole connection made after the address arrives —
// dial, handshake, the balancer's pick — which is waiting for a connection
// too: max 5.1 ms.
const (
	handshakeSlack = 5 * time.Millisecond
	resolveSlack   = 10 * time.Millisecond
)

// feed plays events into a call as grpc-go would, with the handler's clock at
// each event's moment.
type feed struct {
	t0      time.Time
	call    *callStats
	ctx     context.Context
	now     time.Time
	streams *streamGauge
}

func newFeed(invokedAfter time.Duration) *feed {
	// One stream allowed and taken: every wait for headers is for a stream.
	streams := &streamGauge{limit: func() uint32 { return 1 }}
	streams.opened(time.Unix(0, 0))
	f := &feed{t0: time.Unix(1_000_000, 0), call: &callStats{}, streams: streams}
	f.ctx = context.WithValue(context.Background(), callKey{}, f.call)
	f.call.times.invokedAt = f.t0.Add(invokedAfter)

	return f
}

func (f *feed) at(ms int, rpc stats.RPCStats) {
	f.now = f.t0.Add(time.Duration(ms) * time.Millisecond)
	f.handler().HandleRPC(f.ctx, rpc)
}

func (f *feed) handler() handler {
	return handler{streams: f.streams, clock: func() time.Time { return f.now }}
}

// sent puts the headers and the request out at ms.
func (f *feed) sent(ms int) {
	f.at(ms, &stats.OutHeader{Client: true})
	f.at(ms, &stats.OutPayload{Client: true, SentTime: f.t0.Add(time.Duration(ms) * time.Millisecond)})
}

// begin opens an attempt; resolved says grpc-go waited for the resolver
// before it, which it reports in TagRPC, not in Begin.
func (f *feed) begin(ms int, resolved, retry bool) {
	f.now = f.t0.Add(time.Duration(ms) * time.Millisecond)
	h := f.handler()
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
		name    string
		invoked int // ms from t0 to Send's mark
		play    func(f *feed)
		want    time.Duration
	}{
		// Counting the first attempt from invokedAt regardless reads 5.
		{"no wait, no event", -5, func(f *feed) {
			f.begin(0, false, false)
			f.sent(1)
		}, 0},
		// Counting from invokedAt without a resolver wait reads 55.
		{"one blocked pick", -5, func(f *feed) {
			f.begin(0, false, false)
			f.at(50, &stats.DelayedPickComplete{})
			f.sent(50)
		}, 50 * time.Millisecond},
		// Ending the wait at SentAt instead of the event reads 40.
		{"a connection, then a stream", 0, func(f *feed) {
			f.begin(0, false, false)
			f.at(10, &stats.DelayedPickComplete{})
			f.sent(40)
		}, 10 * time.Millisecond},
		// A mutation counting from the first BeginTime reads 120, the last
		// attempt only reads 10, nothing reads 0.
		{"two attempts apart", 0, func(f *feed) {
			f.begin(0, false, false)
			f.at(10, &stats.DelayedPickComplete{})
			f.begin(100, false, true)
			f.at(110, &stats.DelayedPickComplete{})
			f.sent(111)
		}, 20 * time.Millisecond},
		// Resolution took 40 ms before Begin, then the pick waited 10: counted
		// from BeginTime it reads 10.
		{"resolver before the first Begin", -40, func(f *feed) {
			f.begin(0, true, false)
			f.at(10, &stats.DelayedPickComplete{})
			f.sent(10)
		}, 50 * time.Millisecond},
		{"resolver, then no blocked pick", -40, func(f *feed) {
			f.begin(0, true, false)
			f.sent(1)
		}, 40 * time.Millisecond},
		// First attempt waited 50 ms and lost its transport before sending; the
		// retry blocks until the deadline. Not sent, and the 50 ms stay.
		{"measured wait kept on a call that never went out", 0, func(f *feed) {
			f.begin(0, false, false)
			f.at(50, &stats.DelayedPickComplete{})
			f.begin(51, false, true)
			f.at(300, &stats.End{EndTime: f.t0.Add(300 * time.Millisecond)})
		}, 50 * time.Millisecond},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFeed(time.Duration(c.invoked) * time.Millisecond)
			c.play(f)

			got := f.call.read()
			if got.connWait != c.want {
				t.Errorf("connWait = %v, want %v", got.connWait, c.want)
			}
			// The two waits come one after the other, from Send's mark to the
			// request going out.
			if !got.sentAt.IsZero() {
				if sum, total := got.connWait+got.streamWait(), got.sentAt.Sub(got.invokedAt); sum > total {
					t.Errorf("connWait %v + streamWait %v > %v from invokedAt to sentAt", got.connWait, got.streamWait(), total)
				}
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
				go serveAfterData(conn)

				continue
			}
			go func() {
				select {
				case <-g.gate:
				case <-t.Context().Done():
					_ = conn.Close()

					return
				}
				serveAfterData(conn)
			}()
		}
	}()

	g.sender = New(Options{Target: "passthrough:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		// From Begin, not from entering grpc-go: without a resolver wait the
		// call's wait for a connection starts there, and 0.5–2.2 ms of grpc-go
		// before it is not waiting (seen on Windows, 300 runs).
		grpc.WithStatsHandler(beginSignal(g.started)),
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

// serveAfterData answers each call with answerOK once its DATA has arrived.
// Answered on HEADERS, the reply could beat the client's write of the body:
// grpc-go then reports no OutPayload and the call succeeds without SentAt
// (161 of 2000 under -race on a GitHub runner, go1.27.1).
func serveAfterData(conn net.Conn) {
	serveRawData(conn, func(*http2.Framer, uint32, int) {}, func(fr *http2.Framer, stream uint32) bool {
		answerOK(fr, stream, 0)

		return false
	})
}

// answerOK replies with an empty message and OK. A trailers-only OK carries
// no message, which grpc-go rejects for a unary call as Internal.
// beginSignal reports the first attempt's Begin.
type beginSignal chan<- time.Time

func (b beginSignal) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

func (b beginSignal) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (b beginSignal) HandleConn(context.Context, stats.ConnStats) {}

func (b beginSignal) HandleRPC(_ context.Context, rpc stats.RPCStats) {
	if v, ok := rpc.(*stats.Begin); ok && !v.IsTransparentRetryAttempt {
		select {
		case b <- v.BeginTime:
		default:
		}
	}
}

func answerOK(fr *http2.Framer, stream uint32, _ int) {
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, h := range [][2]string{{":status", "200"}, {"content-type", "application/grpc"}} {
		_ = enc.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]})
	}
	_ = fr.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndHeaders: true})
	_ = fr.WriteData(stream, false, []byte{0, 0, 0, 0, 0})
	trailersOnly(fr, stream, codes.OK)
}

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

// sendHeld sends one call, holds the connection for hold after started fires,
// then lets it through.
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
	// The request must have gone out: an unsent call's ConnWait is only a
	// lower bound, and the check below would pass on it for another reason.
	if r.out.SentAt.IsZero() || r.out.Category != engine.CategorySuccess {
		t.Fatalf("SentAt = %v, category = %v (%v): the call did not go out and back", r.out.SentAt, r.out.Category, r.out.Err)
	}
	// Loose: Send's own mark is not in the Outcome. The exact invariant, from
	// invokedAt, is checked on every case of the handler test.
	if total := r.out.SentAt.Sub(before); r.out.ConnWait+r.out.StreamWait > total {
		t.Errorf("ConnWait %v + StreamWait %v > %v from start to sent", r.out.ConnWait, r.out.StreamWait, total)
	}

	return r.out
}

func checkHeldWait(t *testing.T, out engine.Outcome, hold, slack time.Duration) {
	t.Helper()

	t.Logf("over hold: %v", out.ConnWait-hold)

	if out.ConnWait < hold || out.ConnWait > hold+slack {
		t.Errorf("ConnWait = %v, want [%v, %v]", out.ConnWait, hold, hold+slack)
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
			checkHeldWait(t, out, hold, handshakeSlack)
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
	// One read per turn: a second GetState could see Idle already and wait
	// for a change away from it that never comes.
	for s := sender.conn.GetState(); s != connectivity.Idle; s = sender.conn.GetState() {
		if !sender.conn.WaitForStateChange(bounded(t), s) {
			t.Fatal("the channel never went idle")
		}
	}

	out := sendHeld(t, sender, started, func() { close(res.gate) }, hold)
	if res.builds.Load() < 2 {
		t.Fatal("the resolver was not rebuilt: the call did not wait for it")
	}
	checkHeldWait(t, out, hold, resolveSlack)
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
