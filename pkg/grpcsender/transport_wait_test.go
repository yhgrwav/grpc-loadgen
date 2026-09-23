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
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// holdingTarget answers only after release is closed and reports each call
// it has entered, so a test can fill the stream quota without sleeping.
type holdingTarget struct {
	grpc_health_v1.UnimplementedHealthServer

	entered chan struct{}
	release chan struct{}
}

func (h *holdingTarget) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	h.entered <- struct{}{}

	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &grpc_health_v1.HealthCheckResponse{}, nil
}

// oneStreamTarget connects a sender to a server that allows a single
// concurrent stream, and occupies that stream with a call held open until the
// test ends: the next request can only wait for quota.
func oneStreamTarget(t *testing.T) *Sender {
	t.Helper()

	hold := &holdingTarget{entered: make(chan struct{}, 1), release: make(chan struct{})}

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.MaxConcurrentStreams(1))
	grpc_health_v1.RegisterHealthServer(srv, hold)

	go func() { _ = srv.Serve(lis) }()

	sender := New(Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})
	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	held := make(chan struct{})

	t.Cleanup(func() {
		close(hold.release)
		<-held
		_ = sender.Close()
		srv.Stop()
	})

	go func() {
		defer close(held)
		_, _ = sender.Send(context.Background(), request(time.Now()))
	}()

	select {
	case <-hold.entered:
	case <-bounded(t).Done():
		t.Fatal("the holding call never reached the target")
	}

	return sender
}

// Ground: boundary — the request waits for stream quota exactly until its deadline and never
// leaves.
func TestSend_QuotaWaitUntilDeadlineIsNotServiceTime(t *testing.T) {
	const budget = 100 * time.Millisecond

	sender := oneStreamTarget(t)

	req := request(time.Now())
	req.Deadline = req.ScheduledAt.Add(budget)

	out, err := sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if out.Category != engine.CategoryTimeout {
		t.Fatalf("category = %v, want timeout: the request waited for quota until its deadline", out.Category)
	}
	if out.SentAt.IsZero() || out.DoneAt.IsZero() {
		t.Fatalf("SentAt %v / DoneAt %v, want both set: zero SentAt makes the pool book the quota wait as service time",
			out.SentAt, out.DoneAt)
	}
	if !out.SentAt.Equal(out.DoneAt) {
		t.Errorf("SentAt %v != DoneAt %v: the request never left, its service time must be zero",
			out.SentAt, out.DoneAt)
	}
	if out.DoneAt.Before(req.Deadline) {
		t.Errorf("DoneAt %v is before the deadline %v: the quota wait was cut short",
			out.DoneAt, req.Deadline)
	}
	if !out.NotSent {
		t.Errorf("NotSent = false: the request never left, and the engine must not blame the target")
	}
}

// Ground: boundary — the branch "headers out, body stuck" has no end-to-end test: a grpc-go
// server reads the whole body before the handler, so the stand cannot hold a flow-control window
// shut.
func TestTimestamps_WhereAnUnsentTimeoutStops(t *testing.T) {
	base := time.Now()
	header, payload, end := base.Add(10*time.Millisecond), base.Add(20*time.Millisecond), base.Add(100*time.Millisecond)

	tests := []struct {
		name     string
		call     callTimes
		category engine.Category
		wantSent time.Time
		notSent  bool
	}{
		{"payload went out: unchanged", callTimes{headerAt: header, sentAt: payload, doneAt: end}, engine.CategoryTimeout, payload, false},
		// Headers out means the stream existed and the target saw it; waiting on
		// its flow-control window after that is the target's doing.
		{"stream opened, body stuck: from the header", callTimes{headerAt: header, doneAt: end}, engine.CategoryTimeout, header, false},
		{"no stream: quota wait, nothing is service", callTimes{doneAt: end}, engine.CategoryTimeout, end, true},
		{"unreachable: left as is", callTimes{doneAt: end}, engine.CategoryUnreachable, time.Time{}, false},
		{"success: unchanged", callTimes{headerAt: header, sentAt: payload, doneAt: end}, engine.CategorySuccess, payload, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sent, done, notSent := timestamps(tt.call, tt.category)

			if notSent != tt.notSent {
				t.Errorf("not sent = %v, want %v", notSent, tt.notSent)
			}

			if !sent.Equal(tt.wantSent) {
				t.Errorf("SentAt = %v, want %v", sent, tt.wantSent)
			}
			if !done.Equal(end) {
				t.Errorf("DoneAt = %v, want %v", done, end)
			}
		})
	}
}

// Ground: concurrency — dropping the lock goes red under -race (checked 2026-09-22).
// The timings live under this: grpc-go reports a stream from the transport's own
// goroutine, which keeps working on a call the caller has already given up on.
// Reading and writing the same struct at once must therefore be safe.
func TestHandleRPC_WritesWhileTheCallReadsWhatItRecorded(t *testing.T) {
	call := &callStats{}
	ctx := context.WithValue(t.Context(), callKey{}, call)

	written := make(chan struct{})

	go func() {
		defer close(written)

		handler{}.HandleRPC(ctx, &stats.InTrailer{})
		handler{}.HandleRPC(ctx, &stats.End{EndTime: time.Now()})
	}()

	// What Send does the instant Invoke returns.
	_ = call.read()

	<-written

	if times := call.read(); !times.answered {
		t.Errorf("the target's trailer was handled, and the call is not marked answered")
	}
}
