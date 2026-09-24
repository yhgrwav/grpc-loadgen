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
	"testing"
	"time"
)

// warmupRun records one call of each kind scheduled inside a 1s warmup, and one
// success after it. at is the offset from the start each call is scheduled at.
func warmupRun(t *testing.T, calls []Outcome, at []time.Duration) *Stats {
	t.Helper()

	stats := NewStats()
	start := time.Now()
	stats.Start(start, time.Second)

	for i := range calls {
		o := calls[i]
		sched := start.Add(at[i])
		if o.SentAt.IsZero() {
			o.SentAt = sched
		}
		if o.DoneAt.IsZero() {
			o.DoneAt = sched.Add(time.Millisecond)
		}
		stats.Record(Result{Method: "a", ScheduledAt: sched, BegunAt: sched, Deadline: sched.Add(time.Second), Outcome: o})
	}

	end := start.Add(3 * time.Second)
	stats.EndSending(end)
	stats.Finish(end)

	return stats
}

// Warm-up calls went out, and the target saw them: they are counted by the same
// rule as Sent, only apart. Unreachable is sent, a call that never went out is
// not, an aborted one is sent but not failed — as in Sent. The sum then matches
// the target in a run with errors too.
//
// Ground: contract — Report.WarmupSent and WarmupFailed are public.
func TestReport_WarmupIsCountedByTheSameRuleAsSent(t *testing.T) {
	ms := time.Millisecond
	stats := warmupRun(t, []Outcome{
		{Category: CategorySuccess},
		{Category: CategoryOverload, Code: "Unavailable", CodeFromTarget: true},
		{Category: CategoryUnreachable, Code: "Unavailable"},
		{Category: CategoryCutOff, Code: "Internal"},
		{Category: CategoryTimeout, NotSent: true},
		{Category: CategoryAborted},
		{Category: CategorySuccess},
	}, []time.Duration{100 * ms, 200 * ms, 300 * ms, 400 * ms, 500 * ms, 600 * ms, 1500 * ms})

	r := stats.Report()
	if r.WarmupSent != 5 || r.WarmupFailed != 3 {
		t.Errorf("warm-up sent %d, failed %d; want 5 and 3: success, overload, unreachable, cut off, aborted went out; three failed",
			r.WarmupSent, r.WarmupFailed)
	}
	if r.Sent != 1 || r.Failed != 0 {
		t.Errorf("sent %d, failed %d; want 1 and 0: only the call after the warmup", r.Sent, r.Failed)
	}
}

// The window's edge is scheduledAt, as for latency: a call scheduled a
// millisecond before the warmup ends and finished well after it is warmup, and
// is counted once.
//
// Ground: contract — Report.WarmupSent is public.
func TestReport_ACallAcrossTheWarmupEdgeIsWarmupOnce(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, time.Second)

	sched := start.Add(time.Second - time.Millisecond)
	stats.Record(Result{Method: "a", ScheduledAt: sched, BegunAt: sched, Deadline: sched.Add(time.Second),
		Outcome: Outcome{Category: CategorySuccess, SentAt: sched, DoneAt: sched.Add(500 * time.Millisecond)}})
	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	r := stats.Report()
	if r.WarmupSent != 1 || r.Sent != 0 {
		t.Errorf("warm-up sent %d, sent %d; want 1 and 0: scheduled inside the warmup", r.WarmupSent, r.Sent)
	}
	if r.WarmupSent+r.Sent != 1 {
		t.Errorf("counted %d times", r.WarmupSent+r.Sent)
	}
}

// While the warmup runs, the live view has something to show: the calls going out.
//
// Ground: contract — Snapshot.WarmupSent is public.
func TestSnapshot_CountsWarmupCallsGoingOut(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, time.Hour)

	for i := range 3 {
		sched := start.Add(time.Duration(i) * time.Millisecond)
		stats.Record(Result{Method: "a", ScheduledAt: sched, BegunAt: sched, Deadline: sched.Add(time.Second),
			Outcome: Outcome{Category: CategorySuccess, SentAt: sched, DoneAt: sched.Add(time.Millisecond)}})
	}

	if s := stats.Snapshot(); s.WarmupSent != 3 || s.Sent != 0 {
		t.Errorf("snapshot warm-up sent %d, sent %d; want 3 and 0", s.WarmupSent, s.Sent)
	}
}

// Stopped during the warmup: nothing measured, every call that went out is on
// the warm-up line, and the run is incomplete — exit 3, as any stopped run.
//
// Ground: concurrency — calls in flight at the moment of Stop.
func TestStop_DuringTheWarmupKeepsItsCalls(t *testing.T) {
	h := newHoldingSender()
	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: time.Minute, Stages: []Stage{{StartRPS: 200, TargetRPS: 200, Duration: time.Hour}}}},
		Sender:      h,
		MaxInFlight: 20000,
		Warmup:      30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	done := runAsync(t.Context(), eng)

	waitEntered(t, h, 3)
	eng.Stop()
	close(h.release)
	if err := waitRun(t, done); err != nil {
		t.Fatalf("Run after Stop = %v", err)
	}

	r := eng.Report()
	if r.Sent != 0 || r.Failed != 0 {
		t.Errorf("sent %d, failed %d; want 0 and 0: the run never left the warmup", r.Sent, r.Failed)
	}
	if int64(r.WarmupSent) != h.calls.Load() || r.WarmupFailed != 0 {
		t.Errorf("warm-up sent %d, failed %d; the sender saw %d, none failed", r.WarmupSent, r.WarmupFailed, h.calls.Load())
	}
	if !r.Incomplete {
		t.Error("not incomplete: a stopped run exits 3")
	}
}
