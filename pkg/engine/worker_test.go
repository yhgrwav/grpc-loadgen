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
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type senderFunc func(ctx context.Context, req Request) (Outcome, error)

func (f senderFunc) Send(ctx context.Context, req Request) (Outcome, error) {
	return f(ctx, req)
}

func slowSender(d time.Duration) senderFunc {
	return func(ctx context.Context, _ Request) (Outcome, error) {
		select {
		case <-time.After(d):
			return Outcome{Category: CategorySuccess}, nil
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
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

// Ground: contract — WorkerPool is exported.
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
		if r.Category != CategorySuccess {
			t.Errorf("unexpected category: %v", r.Category)
		}
		if r.Latency() <= 0 {
			t.Errorf("latency = %s, want a positive value", r.Latency())
		}
	}
}

// Ground: boundary — latency from BegunAt, the generator's own queue left out, stays green in every
// end-to-end test; latency from SentAt is caught only by the stream-quota stop (mutations
// 2026-09-22). Not a duplicate.
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

// Ground: concurrency — calls overlap in flight.
func TestPoolSendsConcurrently(t *testing.T) {
	const requests = 20

	var inFlight, peak atomic.Int64

	sender := senderFunc(func(_ context.Context, _ Request) (Outcome, error) {
		current := inFlight.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)

		return Outcome{Category: CategorySuccess}, nil
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

// Ground: contract — the pool stops with ErrInFlightCapExceeded once the cap is full. Not exact:
// 16 requests against a cap of 2, so an off-by-one in the check stays green.
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

	if !errors.Is(err, ErrInFlightCapExceeded) {
		t.Fatalf("error = %v, want %v", err, ErrInFlightCapExceeded)
	}
}

// Ground: contract — WorkerPool is exported.
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

// Ground: contract — WorkerPool is exported.
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
			wantErr: ErrInvalidInFlightCap,
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

// Ground: contract — WorkerPool is exported.
func TestPoolPropagatesSenderError(t *testing.T) {
	wantErr := errors.New("sender unusable")

	sender := senderFunc(func(_ context.Context, _ Request) (Outcome, error) {
		return Outcome{}, wantErr
	})

	in := make(chan Request, 1)
	in <- Request{ScheduledAt: time.Now()}
	close(in)

	out := make(chan Result, 1)

	pool := NewWorkerPool(sender, 4)
	err := pool.Run(context.Background(), in, out)

	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}

	select {
	case r := <-out:
		t.Fatalf("got unexpected result %+v, want none", r)
	default:
	}
}

// Ground: contract — WorkerPool is exported.
func TestPoolReportsFailedCallWithoutFailingRun(t *testing.T) {
	callErr := errors.New("call failed")

	sender := senderFunc(func(_ context.Context, _ Request) (Outcome, error) {
		return Outcome{Category: CategoryServerFault, Err: callErr}, nil
	})

	in := make(chan Request, 1)
	in <- Request{ScheduledAt: time.Now()}
	close(in)

	out := make(chan Result, 1)

	pool := NewWorkerPool(sender, 4)
	if err := pool.Run(context.Background(), in, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)

	got := <-out
	if got.Category != CategoryServerFault {
		t.Errorf("category = %v, want %v", got.Category, CategoryServerFault)
	}
	if !errors.Is(got.Err, callErr) {
		t.Errorf("err = %v, want %v", got.Err, callErr)
	}
}

// Ground: boundary — a sender that fills in no timestamps.
func TestPoolFillsInZeroTimestamps(t *testing.T) {
	sender := senderFunc(func(_ context.Context, _ Request) (Outcome, error) {
		return Outcome{Category: CategorySuccess}, nil
	})

	in := make(chan Request, 1)
	in <- Request{ScheduledAt: time.Now()}
	close(in)

	out := make(chan Result, 1)

	pool := NewWorkerPool(sender, 4)
	if err := pool.Run(context.Background(), in, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)

	got := <-out
	if got.SentAt.IsZero() {
		t.Error("SentAt is zero, want the pool to fill it in")
	}
	if got.DoneAt.IsZero() {
		t.Error("DoneAt is zero, want the pool to fill it in")
	}
	if got.Latency() < 0 {
		t.Errorf("Latency() = %s, want non-negative", got.Latency())
	}
	if got.QueueTime() < 0 {
		t.Errorf("QueueTime() = %s, want non-negative", got.QueueTime())
	}
	if got.TransportWait() < 0 {
		t.Errorf("TransportWait() = %s, want non-negative", got.TransportWait())
	}
	if got.ServiceTime() < 0 {
		t.Errorf("ServiceTime() = %s, want non-negative", got.ServiceTime())
	}
}

// Ground: contract — WorkerPool is exported.
func TestResultLatencyComponentsSumToTotal(t *testing.T) {
	scheduledAt := time.Now().Add(-100 * time.Millisecond)
	sentAt := scheduledAt.Add(30 * time.Millisecond)
	doneAt := sentAt.Add(50 * time.Millisecond)

	sender := senderFunc(func(_ context.Context, _ Request) (Outcome, error) {
		return Outcome{Category: CategorySuccess, SentAt: sentAt, DoneAt: doneAt}, nil
	})

	in := make(chan Request, 1)
	in <- Request{ScheduledAt: scheduledAt}
	close(in)

	out := make(chan Result, 1)

	pool := NewWorkerPool(sender, 4)
	if err := pool.Run(context.Background(), in, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(out)

	got := <-out
	if sum := got.QueueTime() + got.TransportWait() + got.ServiceTime(); sum != got.Latency() {
		t.Errorf("QueueTime + TransportWait + ServiceTime = %s, want Latency %s", sum, got.Latency())
	}
}

// Ground: concurrency — releasing a pool slot.
func TestPoolFreesSlotBeforeResultIsDelivered(t *testing.T) {
	sent := make(chan struct{}, 2)

	sender := senderFunc(func(_ context.Context, _ Request) (Outcome, error) {
		sent <- struct{}{}

		return Outcome{Category: CategorySuccess}, nil
	})

	in := make(chan Request)
	out := make(chan Result)

	pool := NewWorkerPool(sender, 1)

	done := make(chan error, 1)
	go func() { done <- pool.Run(context.Background(), in, out) }()

	in <- Request{ScheduledAt: time.Now()}
	<-sent

	// Nobody reads out, so the first result is still queued. The slot it used
	// must already be free: in-flight means "in flight", not "waiting to be
	// recorded".
	waitForNoneInFlight(t, pool)

	// With the slot free, a second request must launch even though the
	// collector has read nothing.
	in <- Request{ScheduledAt: time.Now()}
	<-sent

	<-out
	<-out
	close(in)

	if err := <-done; err != nil {
		t.Fatalf("run: %v, want nil: a busy result consumer must not hold in-flight slots", err)
	}
}

func waitForNoneInFlight(t *testing.T, pool *WorkerPool) {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		if pool.inFlightCount() == 0 {
			return
		}

		select {
		case <-deadline:
			t.Fatal("in-flight slot stays held while the result waits for the collector")
		default:
			runtime.Gosched()
		}
	}
}

// Ground: concurrency — cancellation racing a call in flight.
func TestPoolDoesNotBlameSenderForOwnCancellation(t *testing.T) {
	const requests = 4

	sentinel := errors.New("sender reports cancel")
	started := make(chan struct{}, requests)

	sender := senderFunc(func(ctx context.Context, _ Request) (Outcome, error) {
		started <- struct{}{}
		<-ctx.Done()
		return Outcome{}, fmt.Errorf("%w: %w", sentinel, ctx.Err())
	})

	in := make(chan Request, requests)
	for range requests {
		in <- Request{ScheduledAt: time.Now()}
	}
	close(in)

	out := make(chan Result, requests)

	ctx, cancel := context.WithCancel(context.Background())

	pool := NewWorkerPool(sender, requests)

	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx, in, out) }()

	for range requests {
		<-started
	}
	cancel()

	err := <-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}
	if errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want cancellation not attributed to the sender", err)
	}
}

// Ground: concurrency — a stalled reader of results.
func TestPoolReturnsWhenNobodyReadsResults(t *testing.T) {
	in := make(chan Request, 8)
	for range 8 {
		in <- Request{ScheduledAt: time.Now()}
	}
	close(in)

	out := make(chan Result)

	pool := NewWorkerPool(slowSender(10*time.Millisecond), 1)

	done := make(chan error, 1)
	go func() {
		done <- pool.Run(context.Background(), in, out)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInFlightCapExceeded) {
			t.Fatalf("error = %v, want %v", err, ErrInFlightCapExceeded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return while results were left unread")
	}
}
