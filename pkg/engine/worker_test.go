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

package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type senderFunc func(ctx context.Context, req Request) error

func (f senderFunc) Send(ctx context.Context, req Request) error {
	return f(ctx, req)
}

func slowSender(d time.Duration) senderFunc {
	return func(ctx context.Context, _ Request) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func drain(out <-chan Result, done *sync.WaitGroup) *[]Result {
	got := make([]Result, 0, 64)

	done.Add(1)
	go func() {
		defer done.Done()
		for r := range out {
			got = append(got, r)
		}
	}()

	return &got
}

func TestPoolReportsEveryRequest(t *testing.T) {
	in := make(chan Request, 8)
	out := make(chan Result, 8)

	for i := range 5 {
		in <- Request{ScheduledAt: time.Now().Add(-time.Duration(i) * time.Millisecond)}
	}
	close(in)

	var collected sync.WaitGroup
	got := drain(out, &collected)

	pool := NewWorkerPool(slowSender(time.Millisecond), 10)
	if err := pool.Run(context.Background(), in, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)
	collected.Wait()

	if len(*got) != 5 {
		t.Fatalf("got %d results, want 5", len(*got))
	}
	for _, r := range *got {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
		}
		if r.Latency() <= 0 {
			t.Errorf("latency = %s, want a positive value", r.Latency())
		}
	}
}

func TestPoolMeasuresLatencyFromScheduledTime(t *testing.T) {
	scheduledAt := time.Now().Add(-100 * time.Millisecond)

	in := make(chan Request, 1)
	in <- Request{ScheduledAt: scheduledAt}
	close(in)

	out := make(chan Result, 1)

	pool := NewWorkerPool(slowSender(10*time.Millisecond), 4)
	if err := pool.Run(context.Background(), in, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)

	got := <-out

	if got.Latency() < 100*time.Millisecond {
		t.Errorf("latency = %s, want at least the 100ms the request waited", got.Latency())
	}
	if got.ServiceTime() >= got.Latency() {
		t.Errorf("service time %s should be shorter than latency %s", got.ServiceTime(), got.Latency())
	}
}

func TestPoolSendsConcurrently(t *testing.T) {
	const requests = 20

	var inFlight, peak atomic.Int64

	sender := senderFunc(func(_ context.Context, _ Request) error {
		current := inFlight.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)

		return nil
	})

	in := make(chan Request, requests)
	for range requests {
		in <- Request{ScheduledAt: time.Now()}
	}
	close(in)

	out := make(chan Result, requests)

	pool := NewWorkerPool(sender, requests)
	if err := pool.Run(context.Background(), in, out); err != nil {
		t.Fatalf("run: %v", err)
	}

	if peak.Load() < 2 {
		t.Errorf("peak in-flight = %d, want the pool to send in parallel", peak.Load())
	}
}

func TestPoolFailsWhenInFlightLimitIsReached(t *testing.T) {
	const limit = 2

	in := make(chan Request, 16)
	for range 16 {
		in <- Request{ScheduledAt: time.Now()}
	}
	close(in)

	out := make(chan Result, 16)

	pool := NewWorkerPool(slowSender(time.Second), limit)
	err := pool.Run(context.Background(), in, out)

	if !errors.Is(err, ErrNoInFlightRoom) {
		t.Fatalf("error = %v, want %v", err, ErrNoInFlightRoom)
	}
}

func TestPoolStopsOnCancel(t *testing.T) {
	in := make(chan Request)
	out := make(chan Result, 4)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pool := NewWorkerPool(slowSender(time.Millisecond), 4)
	err := pool.Run(ctx, in, out)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
}

func TestPoolRejectsBadSetup(t *testing.T) {
	tests := []struct {
		name    string
		pool    *WorkerPool
		wantErr error
	}{
		{
			name:    "no sender",
			pool:    NewWorkerPool(nil, 4),
			wantErr: ErrNoSender,
		},
		{
			name:    "zero in-flight limit",
			pool:    NewWorkerPool(slowSender(time.Millisecond), 0),
			wantErr: ErrNoInFlightRoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make(chan Request)
			close(in)

			err := tt.pool.Run(context.Background(), in, make(chan Result, 1))

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
