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

package stand

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// bufSize is the in-process listener's buffer. Large enough that a burst of
// requests is never held back by the transport: the stand must add no delay
// of its own beyond the one it was told to add.
const bufSize = 1024 * 1024

// Call is the arrival a behavior is chosen for.
type Call struct {
	// N is the arrival number, 1 for the first call the stand ever sees.
	// Arrival order is not send order: concurrent calls get their numbers in
	// whatever order they reach the handler.
	N int
	// Since is how long after the first arrival this one came in.
	Since time.Duration
}

// Behavior is what the stand does with one call.
type Behavior struct {
	// Delay holds the answer back for this long; anything not positive answers
	// at once.
	Delay time.Duration
	// Hang holds the call until the caller gives up or the stand stops, and
	// overrides Delay. Nothing the caller can use ever comes back, so what the
	// report says about such a call rests on the caller's own deadline.
	Hang bool
	// Code answers with this status instead of a reply, after Delay.
	// codes.OK replies normally.
	Code codes.Code
}

// Answer picks the behavior for one call. It is called from the handler, so
// it must be safe for concurrent use; the helpers below are pure functions of
// the Call and need no synchronisation.
type Answer func(Call) Behavior

// Constant answers every call after the same delay.
func Constant(d time.Duration) Answer {
	return func(Call) Behavior { return Behavior{Delay: d} }
}

// Hanging never answers: every call waits out its own deadline.
func Hanging() Answer {
	return func(Call) Behavior { return Behavior{Hang: true} }
}

// FailEvery answers every n-th arrival with code after delay and the rest
// normally, so the number of failures in a run of N calls is exactly N/n.
// n below 1 fails nothing.
func FailEvery(n int, code codes.Code, delay time.Duration) Answer {
	return func(c Call) Behavior {
		behavior := Behavior{Delay: delay}
		if n >= 1 && c.N%n == 0 {
			behavior.Code = code
		}

		return behavior
	}
}

// Slowing answers the first n arrivals after fast and every later one after
// slow: the target's service time changes in the middle of the run, and the
// report has to show both parts instead of one number in between.
func Slowing(n int, fast, slow time.Duration) Answer {
	return func(c Call) Behavior {
		if c.N <= n {
			return Behavior{Delay: fast}
		}

		return Behavior{Delay: slow}
	}
}

// Frozen answers after delay, except that every call arriving in the window
// [from, from+length) after the first arrival is held until the window ends:
// the target stops in the middle of the run and then lets everything go. The
// call that arrived first in the window waited length; a report showing the
// delay for it has left the stop out.
func Frozen(from, length, delay time.Duration) Answer {
	return func(c Call) Behavior {
		end := from + length
		if c.Since >= from && c.Since < end {
			return Behavior{Delay: end - c.Since + delay}
		}

		return Behavior{Delay: delay}
	}
}

// HangingFrom answers after delay until from after the first arrival, and
// never answers from then on: the target stops in the middle of the run and
// does not come back.
func HangingFrom(from, delay time.Duration) Answer {
	return func(c Call) Behavior {
		if c.Since >= from {
			return Behavior{Hang: true}
		}

		return Behavior{Delay: delay}
	}
}

// Stand serves the gRPC health service and answers the way it was told to.
type Stand struct {
	grpc_health_v1.UnimplementedHealthServer

	answer Answer
	srv    *grpc.Server
	lis    net.Listener
	// buf is set when the stand listens in process; nil on a network listener.
	buf      *bufconn.Listener
	stopped  chan struct{}
	stopOnce sync.Once

	mu       sync.Mutex
	first    time.Time
	arrivals []time.Time
	holds    []time.Duration
}

// Start serves a stand on an in-process listener until Stop. A nil answer
// means every call is answered at once.
func Start(answer Answer) *Stand { return StartWith(answer) }

// StartWith is Start with server options, such as grpc.MaxConcurrentStreams:
// a stream quota makes calls wait inside the generator before they are sent,
// which is where coordinated omission hides.
func StartWith(answer Answer, opts ...grpc.ServerOption) *Stand {
	buf := bufconn.Listen(bufSize)
	s := StartOn(buf, answer, opts...)
	s.buf = buf

	return s
}

// StartOn serves a stand on lis until Stop, so a separate process, such as
// another load tool, can reach it over the network.
func StartOn(lis net.Listener, answer Answer, opts ...grpc.ServerOption) *Stand {
	if answer == nil {
		answer = Constant(0)
	}

	s := &Stand{
		answer:  answer,
		srv:     grpc.NewServer(opts...),
		lis:     lis,
		stopped: make(chan struct{}),
	}

	grpc_health_v1.RegisterHealthServer(s.srv, s)
	reflection.Register(s.srv)

	go func() { _ = s.srv.Serve(s.lis) }()

	return s
}

// Target is the address to dial. In process it resolves to nothing on its
// own: the connection is made by DialOption.
func (s *Stand) Target() string {
	if s.buf == nil {
		return s.lis.Addr().String()
	}

	return "passthrough:///stand"
}

// Method is the full gRPC path the stand answers on.
func (s *Stand) Method() string { return "/grpc.health.v1.Health/Check" }

// DialOption points a client at this stand instead of the network.
func (s *Stand) DialOption() grpc.DialOption {
	if s.buf == nil {
		return grpc.EmptyDialOption{}
	}

	return grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return s.buf.DialContext(ctx)
	})
}

// Stop releases every held call and shuts the server down. Safe to call more
// than once. Held calls are released before the server goes away, so no
// handler outlives the stand waiting for a timer.
func (s *Stand) Stop() {
	s.stopOnce.Do(func() { close(s.stopped) })
	s.srv.Stop()
}

// Arrivals reports when each call reached the stand, in arrival order. The
// moment is recorded before the stand waits, so calls still being held are in
// the list: this is the record of what the generator actually sent and when,
// taken outside the generator.
func (s *Stand) Arrivals() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.arrivals)
}

// Holds reports how long the stand held each call it answered, a refusal
// included, in the order the answers went out. A call that hung or that the
// caller gave up on is not in the list: nothing was answered.
func (s *Stand) Holds() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.holds)
}

func (s *Stand) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	arrivedAt := time.Now()
	behavior := s.answer(s.arrived(arrivedAt))

	if behavior.Hang {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.stopped:
			return nil, status.Error(codes.Unavailable, "stand stopped")
		}
	}

	if behavior.Delay > 0 {
		timer := time.NewTimer(behavior.Delay)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.stopped:
			return nil, status.Error(codes.Unavailable, "stand stopped")
		}
	}

	s.mu.Lock()
	s.holds = append(s.holds, time.Since(arrivedAt))
	s.mu.Unlock()

	if behavior.Code != codes.OK {
		return nil, status.Error(behavior.Code, "as the stand was told")
	}

	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func (s *Stand) arrived(at time.Time) Call {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.first.IsZero() {
		s.first = at
	}
	s.arrivals = append(s.arrivals, at)

	return Call{N: len(s.arrivals), Since: at.Sub(s.first)}
}
