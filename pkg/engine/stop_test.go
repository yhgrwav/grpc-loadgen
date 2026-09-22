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
	"sync/atomic"
	"testing"
	"time"
)

// holdingSender keeps every call open until release is closed, its deadline
// passes, or ctx is cancelled, and signals each call it has entered.
type holdingSender struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func newHoldingSender() *holdingSender {
	return &holdingSender{entered: make(chan struct{}, 1024), release: make(chan struct{})}
}

func (h *holdingSender) Send(ctx context.Context, req Request) (Outcome, error) {
	h.calls.Add(1)
	h.entered <- struct{}{}

	sentAt := time.Now()

	var deadline <-chan time.Time
	if !req.Deadline.IsZero() {
		timer := time.NewTimer(time.Until(req.Deadline))
		defer timer.Stop()
		deadline = timer.C
	}

	select {
	case <-h.release:
		return Outcome{Category: CategorySuccess, SentAt: sentAt, DoneAt: time.Now()}, nil
	case <-deadline:
		return Outcome{Category: CategoryTimeout, SentAt: sentAt, DoneAt: time.Now()}, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

func stopEngine(t *testing.T, sender Sender, timeout time.Duration) *Engine {
	t.Helper()

	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: timeout, Stages: []Stage{{StartRPS: 200, TargetRPS: 200, Duration: time.Hour}}}},
		Sender:      sender,
		MaxInFlight: 20000,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	return eng
}

// runAsync starts the engine and returns the channel its Run result lands on.
func runAsync(ctx context.Context, eng *Engine) <-chan error {
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()

	return done
}

func waitEntered(t *testing.T, h *holdingSender, n int) {
	t.Helper()

	limit := time.After(5 * time.Second)
	for range n {
		select {
		case <-h.entered:
		case <-limit:
			t.Fatalf("fewer than %d calls reached the sender", n)
		}
	}
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

// Ground: concurrency — calls in flight at the moment of Stop.
func TestStop_InFlightFinishAndAreRecorded(t *testing.T) {
	h := newHoldingSender()
	eng := stopEngine(t, h, time.Minute)
	done := runAsync(t.Context(), eng)

	waitEntered(t, h, 3)
	eng.Stop()
	close(h.release)

	if err := waitRun(t, done); err != nil {
		t.Fatalf("Run after Stop = %v, want nil: a stop is not an error", err)
	}

	report := eng.Report()
	if int64(report.Sent) != h.calls.Load() {
		t.Errorf("recorded %d, sender saw %d: every in-flight call must reach the report", report.Sent, h.calls.Load())
	}
	if report.Failed != 0 || report.Aborted != 0 {
		t.Errorf("failed %d, aborted %d, want 0 and 0: in-flight calls finished normally", report.Failed, report.Aborted)
	}
}

// Ground: contract — Stop and abort are how a library caller ends a run.
func TestStop_SchedulesNothingNew(t *testing.T) {
	h := newHoldingSender()
	eng := stopEngine(t, h, time.Minute)
	done := runAsync(t.Context(), eng)

	waitEntered(t, h, 3)
	eng.Stop()
	atStop := h.calls.Load()
	close(h.release)
	_ = waitRun(t, done)

	// A call already past the scheduler at the moment of Stop may still land;
	// a stream of new ones may not. 200 RPS gives one per 5 ms.
	if after := h.calls.Load(); after > atStop+1 {
		t.Errorf("sender saw %d calls after Stop, had %d at Stop: scheduling went on", after, atStop)
	}
}

// Ground: contract — a stop drains no longer than the timeout, and calls it cut stay as censored.
func TestStop_WaitsNoLongerThanTheDeadline(t *testing.T) {
	const timeout = 150 * time.Millisecond

	h := newHoldingSender() // never released: only deadlines end the calls
	eng := stopEngine(t, h, timeout)
	done := runAsync(t.Context(), eng)

	waitEntered(t, h, 3)
	stopped := time.Now()
	eng.Stop()

	if err := waitRun(t, done); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if waited := time.Since(stopped); waited > timeout+time.Second {
		t.Errorf("stop waited %v, want about the %v timeout at most", waited, timeout)
	}

	var censored int
	for _, m := range eng.Report().Methods {
		censored += m.Censored
	}
	if censored == 0 {
		t.Error("no censored observations: calls that hit the deadline during the stop were lost")
	}
}

// Ground: concurrency — Stop racing the start and the end of a run.
func TestStop_IsSafeAtAnyTime(t *testing.T) {
	h := newHoldingSender()
	close(h.release)
	eng := stopEngine(t, h, time.Minute)

	eng.Stop() // before Run
	eng.Stop()

	if err := waitRun(t, runAsync(t.Context(), eng)); err != nil {
		t.Fatalf("Run after an early Stop = %v, want nil", err)
	}

	eng.Stop() // after Run
}

// Ground: contract — Stop and abort are how a library caller ends a run.
func TestAbort_InFlightAreCensoredNotLost(t *testing.T) {
	h := newHoldingSender()
	eng := stopEngine(t, h, time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	done := runAsync(ctx, eng)

	waitEntered(t, h, 3)
	eng.Stop()
	cancel()

	if err := waitRun(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run after abort = %v, want context.Canceled", err)
	}

	report := eng.Report()
	if int64(report.Sent) != h.calls.Load() {
		t.Errorf("recorded %d, sender saw %d: aborted calls vanished", report.Sent, h.calls.Load())
	}
	if int64(report.Aborted) != h.calls.Load() {
		t.Errorf("aborted = %d, want %d", report.Aborted, h.calls.Load())
	}
	if report.Failed != 0 {
		t.Errorf("failed = %d, want 0: an abort is not the target's failure", report.Failed)
	}

	var censored int
	for _, m := range report.Methods {
		censored += m.Censored
	}
	if int64(censored) != h.calls.Load() {
		t.Errorf("censored = %d, want %d: an aborted call lasted at least until the abort", censored, h.calls.Load())
	}
}

// Ground: boundary — the censoring threshold of a cut-off call is the abort moment.
func TestAbortedResult_ThresholdIsTimeUntilTheAbort(t *testing.T) {
	scheduled := time.Now()
	r := Result{
		ScheduledAt: scheduled,
		Deadline:    scheduled.Add(time.Minute),
		Outcome:     Outcome{Category: CategoryAborted, DoneAt: scheduled.Add(300 * time.Millisecond)},
	}

	// The deadline is a minute away, but the call was only watched for 300 ms.
	if got := r.CensorThreshold(); got != 300*time.Millisecond {
		t.Errorf("threshold = %v, want 300ms: nothing is known past the abort", got)
	}
}

// lateSender holds calls until ctx is cancelled and then returns slowly for
// every other call, the way cancellation reaches goroutines at different times.
type lateSender struct {
	entered chan struct{}
	n       atomic.Int64
}

func (l *lateSender) Send(ctx context.Context, _ Request) (Outcome, error) {
	i := l.n.Add(1)
	l.entered <- struct{}{}

	<-ctx.Done()
	if i%2 == 0 {
		time.Sleep(30 * time.Millisecond) // the lag under test, not synchronisation
	}

	return Outcome{}, ctx.Err()
}

// Ground: concurrency — every cut-off call gets the one abort moment, not its own goroutine's read
// of the clock.
func TestAbort_OneMomentForEveryInFlightCall(t *testing.T) {
	sender := &lateSender{entered: make(chan struct{}, 16)}
	pool := NewWorkerPool(sender, 16)

	in := make(chan Request, 4)
	out := make(chan Result, 16)
	base := time.Now().Add(-time.Second) // in the past: a future ScheduledAt is a negative latency
	for i := range 4 {
		in <- Request{Method: "a.B/One", ScheduledAt: base.Add(-time.Duration(i) * 10 * time.Millisecond), Deadline: base.Add(time.Minute)}
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx, in, out) }()

	for range 4 {
		select {
		case <-sender.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("calls did not reach the sender")
		}
	}

	cancel()
	cancelled := time.Now()
	_ = waitRun(t, done)
	close(out)

	var got []Result
	for r := range out {
		got = append(got, r)
	}
	if len(got) != 4 {
		t.Fatalf("got %d results, want 4: aborted calls must not be dropped", len(got))
	}

	for _, r := range got {
		if r.Category != CategoryAborted {
			t.Fatalf("category = %v, want aborted", r.Category)
		}
		if !r.DoneAt.Equal(got[0].DoneAt) {
			t.Errorf("DoneAt %v differs from %v: every aborted call must stop at the one abort moment",
				r.DoneAt, got[0].DoneAt)
		}
		// The abort moment is taken when the cancellation is seen, a hair after
		// cancel returns; the 30 ms lag of a goroutine must not be in it.
		if lag := r.DoneAt.Sub(cancelled); lag > 15*time.Millisecond {
			t.Errorf("DoneAt is %v after the cancel: the lag of the goroutine leaked into the threshold", lag)
		}
		if want := r.DoneAt.Sub(r.ScheduledAt); r.CensorThreshold() != want {
			t.Errorf("threshold = %v, want %v from its own ScheduledAt", r.CensorThreshold(), want)
		}
	}
}

// Ground: contract — Stop and abort are how a library caller ends a run.
func TestStop_MakesTheRunIncomplete(t *testing.T) {
	h := newHoldingSender()
	eng := stopEngine(t, h, time.Minute)
	done := runAsync(t.Context(), eng)

	waitEntered(t, h, 1)
	eng.Stop()
	close(h.release)
	_ = waitRun(t, done)

	// Not the target's failure, but a run planned for an hour that stopped
	// early: CI must not get a green result from it.
	if !eng.Report().Incomplete {
		t.Error("Incomplete = false after Stop, want true")
	}
}

// Ground: contract — Stop and abort are how a library caller ends a run.
func TestAbort_MakesTheRunIncomplete(t *testing.T) {
	h := newHoldingSender()
	eng := stopEngine(t, h, time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	done := runAsync(ctx, eng)

	waitEntered(t, h, 1)
	cancel()
	_ = waitRun(t, done)

	if !eng.Report().Incomplete {
		t.Error("Incomplete = false after an abort, want true")
	}
}

// Ground: contract — Stop and abort are how a library caller ends a run.
func TestRun_FinishedAsPlannedIsComplete(t *testing.T) {
	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: time.Second, Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 50 * time.Millisecond}}}},
		Sender:      FakeSender{Delay: time.Millisecond},
		MaxInFlight: 128,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := eng.Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}
	eng.Stop() // after the end: changes nothing

	if eng.Report().Incomplete {
		t.Error("Incomplete = true for a run that finished as planned")
	}
}
