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
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// attempts replays grpc-go's stats events for one call whose first attempt
// went into a connection that died before the target read it. grpc-go then
// retries transparently and reports every attempt with its own Begin and End
// (grpc v1.84.0, stream.go newAttemptLocked and csAttempt.finish).
type attempts struct {
	h    handler
	ctx  context.Context
	call *callStats
	t0   time.Time
}

func newAttempts() (*attempts, *Sender) {
	s := New(Options{})
	call := &callStats{}

	return &attempts{
		h:    handler{streams: s.streams},
		ctx:  context.WithValue(context.Background(), callKey{}, call),
		call: call,
		t0:   time.Now().Add(-time.Second),
	}, s
}

func (a *attempts) at(ms int) time.Time { return a.t0.Add(time.Duration(ms) * time.Millisecond) }

func (a *attempts) begin(ms int, retry bool) {
	a.h.HandleRPC(a.ctx, &stats.Begin{Client: true, BeginTime: a.at(ms), IsTransparentRetryAttempt: retry})
}

func (a *attempts) header() { a.h.HandleRPC(a.ctx, &stats.OutHeader{Client: true}) }

func (a *attempts) payload(ms int) {
	a.h.HandleRPC(a.ctx, &stats.OutPayload{Client: true, SentTime: a.at(ms)})
}

func (a *attempts) trailer() { a.h.HandleRPC(a.ctx, &stats.InTrailer{Client: true}) }

func (a *attempts) end(ms int) { a.h.HandleRPC(a.ctx, &stats.End{Client: true, EndTime: a.at(ms)}) }

// Ground: concurrency — a transparent retry needs a stream written into a connection that dies
// before the target reads it; no end-to-end test times a drop that exactly. CI hit it once in
// TestSend_UnsentWhileReconnectingIsBlockedOnTheConnection (go 1.25, 2026-09-23): the gauge read
// -1 after the calls returned.
func TestRetry_AnAttemptWithoutHeadersClosesNoStream(t *testing.T) {
	a, s := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(5, true)
	a.end(100)

	if n := s.OpenStreams(); n != 0 {
		t.Errorf("open streams %d after both attempts ended, want 0", n)
	}
}

// Ground: concurrency — as above. The first attempt's headers went into a dead connection the
// target never read; the call is unsent if the retry got no stream, and a timeout blamed on the
// target otherwise.
func TestRetry_TheLastAttemptDecidesWhetherTheCallWentOut(t *testing.T) {
	a, _ := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(5, true)
	a.end(100)

	if _, _, notSent := timestamps(a.call.read(), engine.CategoryTimeout); !notSent {
		t.Error("not sent = false: only the dead connection ever saw the headers")
	}
}

// Ground: concurrency — as above. The target never read the first attempt even when its body
// went out too: grpc-go retries transparently only a stream the target did not process.
func TestRetry_ABodyTheTargetNeverReadDoesNotMakeTheCallSent(t *testing.T) {
	a, _ := newAttempts()

	a.begin(0, false)
	a.header()
	a.payload(1)
	a.end(5)
	a.begin(5, true)
	a.end(100)

	if _, _, notSent := timestamps(a.call.read(), engine.CategoryTimeout); !notSent {
		t.Error("not sent = false: only the dead connection ever saw the body")
	}
}

// Ground: concurrency — as above; when the retry does go out, the call's times are the retry's.
func TestRetry_ARetryThatWentOutCarriesItsOwnTimes(t *testing.T) {
	a, s := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(5, true)
	a.header()
	a.payload(7)
	a.trailer()
	a.end(30)

	times := a.call.read()

	if !times.sentAt.Equal(a.at(7)) {
		t.Errorf("sent at %v, want the retry's %v", times.sentAt.Sub(a.t0), 7*time.Millisecond)
	}
	if !times.answered {
		t.Error("answered = false after the retry's trailer")
	}
	if n := s.OpenStreams(); n != 0 {
		t.Errorf("open streams %d, want 0", n)
	}
	if !times.begunAt.Equal(a.at(0)) {
		t.Errorf("begun at %v, want the first attempt's 0s: the call has waited since then", times.begunAt.Sub(a.t0))
	}
}

// Ground: concurrency — as above. The first attempt's time went into a connection that died, not
// into a wait for a stream: the retry's wait for one starts at the retry.
func TestRetry_TheWaitForAStreamStartsAtTheRetry(t *testing.T) {
	a, _ := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(40, true)

	if got := a.call.read().waitFrom(); !got.Equal(a.at(40)) {
		t.Errorf("wait from %v, want the retry's 40ms", got.Sub(a.t0))
	}
}

// Ground: concurrency — as above. Whether the connection was full is asked over the wait of the
// attempt that sent the headers: the first attempt met a full connection, the retry did not, so
// the call did not wait for a stream.
func TestRetry_FullnessIsAskedOverTheRetrysOwnWait(t *testing.T) {
	a, s := newAttempts()
	s.tracker.announced(handshake{limit: 1, announced: true})

	s.streams.opened(a.at(0)) // another call holds the only stream
	a.begin(0, false)
	a.header()
	a.end(5)
	s.streams.closed(a.at(10))
	a.begin(20, true)
	a.header()
	a.end(30)

	times := a.call.read()
	if times.streamFull || times.streamWait() != 0 {
		t.Errorf("full %v, wait %v: the retry never met a full connection", times.streamFull, times.streamWait())
	}
	if n := s.OpenStreams(); n != 0 {
		t.Errorf("open streams %d, want 0", n)
	}
}

// Ground: signal grpc-go v1.84.0 — stream.go:819 retries by a service config retryPolicy unless
// retries are disabled; such a retry may follow an attempt the target served. The target here
// fails the first call with UNAVAILABLE under a policy that would retry it: it must see one call.
func TestSend_AServiceConfigRetryPolicyIsNotApplied(t *testing.T) {
	target := &failingOnce{}

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, target)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	sender := New(Options{Target: "passthrough:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithDefaultServiceConfig(`{"methodConfig": [{
		"name": [{"service": "grpc.health.v1.Health"}],
		"retryPolicy": {"maxAttempts": 3, "initialBackoff": "0.01s", "maxBackoff": "0.01s",
			"backoffMultiplier": 1, "retryableStatusCodes": ["UNAVAILABLE"]}}]}`),
	}})
	t.Cleanup(func() { _ = sender.Close() })

	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}
	if n := target.calls.Load(); n != 1 {
		t.Errorf("the target saw %d calls, want 1: the policy retried", n)
	}
}

type failingOnce struct {
	grpc_health_v1.UnimplementedHealthServer

	calls atomic.Int32
}

func (f *failingOnce) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	if f.calls.Add(1) == 1 {
		return nil, status.Error(codes.Unavailable, "first call fails")
	}

	return &grpc_health_v1.HealthCheckResponse{}, nil
}
