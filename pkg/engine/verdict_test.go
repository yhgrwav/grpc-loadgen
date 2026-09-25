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
	"testing"
	"time"
)

// Ground: contract — a timeout of a call that went out is its own outcome on a Second, apart
// from the target's refusals: "no answer within T" is what the statement about the target says.
func TestTimeline_TimeoutsOfSentCallsAreTheirOwnOutcome(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, 0, 500*time.Millisecond, CategoryTimeout))
	stats.Record(call(start, 0, time.Millisecond, CategoryOverload))

	got := seconds(t, stats)[0]
	if got.TimedOut != 1 || (got.Overload+got.Failure) != 1 {
		t.Errorf("timed out %d, target failed %d; want 1 and 1", got.TimedOut, (got.Overload + got.Failure))
	}

	m := stats.Report().Methods[0]
	if m.TimedOut != 1 || m.UnsentTimedOut != 0 {
		t.Errorf("method: timed out %d, unsent timed out %d; want 1 and 0", m.TimedOut, m.UnsentTimedOut)
	}
}

// Ground: contract — a timeout that never went out is the connection's or the generator's, and
// is counted apart from the target's.
func TestStats_UnsentTimeoutsAreCountedApart(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(unsent(start, 0, time.Millisecond))
	stats.Record(unsent(start, 0, -time.Millisecond))

	m := stats.Report().Methods[0]
	if m.TimedOut != 0 || m.UnsentTimedOut != 2 {
		t.Errorf("timed out %d, unsent timed out %d; want 0 and 2", m.TimedOut, m.UnsentTimedOut)
	}
}

// Ground: hot path — the new branches of Record, a timeout and an abort past the deadline,
// allocate nothing either.
func TestStats_RecordOfTimeoutsDoesNotAllocate(t *testing.T) {
	start := time.Now()
	stats := NewStats()
	stats.Reserve(time.Hour, "a")
	stats.Start(start, 0)

	timeout := call(start, 500*time.Millisecond, 700*time.Millisecond, CategoryTimeout)
	timeout.Deadline = start.Add(690 * time.Millisecond)
	aborted := call(start, 500*time.Millisecond, 900*time.Millisecond, CategoryAborted)
	aborted.Deadline = start.Add(690 * time.Millisecond)

	if allocs := testing.AllocsPerRun(1000, func() { stats.Record(timeout); stats.Record(aborted) }); allocs != 0 {
		t.Errorf("Record allocates %v times per call, want 0", allocs)
	}
}

// Ground: contract — how late calls started, and how late a timeout returned past its deadline.
func TestStats_StartLagAndLateCancel(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for i := range 100 {
		r := call(start, 0, 10*time.Millisecond, CategorySuccess)
		r.BegunAt = r.ScheduledAt.Add(time.Duration(i+1) * time.Millisecond)
		stats.Record(r)
	}

	late := call(start, 0, 530*time.Millisecond, CategoryTimeout)
	late.Deadline = start.Add(500 * time.Millisecond)
	stats.Record(late)

	// An aborted call ends at the abort moment, not when it returned: how far
	// that is past its deadline says nothing about cancellation.
	cut := call(start, 0, 2*time.Second, CategoryAborted)
	cut.Deadline = start.Add(500 * time.Millisecond)
	stats.Record(cut)

	report := stats.Report()
	if got := report.StartLagMax; got != 100*time.Millisecond {
		t.Errorf("start lag max = %v, want 100ms", got)
	}
	if got := report.StartLagP99; !got.Defined || got.Value < 99*time.Millisecond || got.Value > 101*time.Millisecond {
		t.Errorf("start lag p99 = %+v, want about 99ms", got)
	}
	if got := report.LateCancelMax; got != 30*time.Millisecond {
		t.Errorf("late cancel max = %v, want 30ms: the abort is not a late cancel", got)
	}
}

// answeringIn answers calls scheduled before from at once, and holds the rest
// until their deadline: the target falls silent at from.
type answeringIn struct {
	start *time.Time
	from  time.Duration
}

func (a answeringIn) Send(ctx context.Context, req Request) (Outcome, error) {
	begun := time.Now()
	if req.ScheduledAt.Sub(*a.start) < a.from {
		return Outcome{Category: CategorySuccess, SentAt: begun, DoneAt: time.Now()}, nil
	}

	select {
	case <-time.After(time.Until(req.Deadline)):
		return Outcome{Category: CategoryTimeout, SentAt: begun, DoneAt: time.Now()}, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

// Ground: contract — SilentFrom counts seconds by planned time with warmup in the numbering,
// and the rate range covers only the stages from there on.
func TestReport_SilentFromAndItsRates(t *testing.T) {
	start := time.Now()
	sender := answeringIn{start: &start, from: 2 * time.Second}

	eng, err := New(Options{
		Calls: []Call{{Method: "a", Timeout: 100 * time.Millisecond, Stages: []Stage{
			{StartRPS: 20, TargetRPS: 20, Duration: 2 * time.Second},
			{StartRPS: 40, TargetRPS: 40, Duration: time.Second},
		}}},
		Sender:      sender,
		MaxInFlight: 100,
		Warmup:      time.Second,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	start = time.Now()
	if err := eng.Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	m := eng.Report().Methods[0]
	if m.SilentFrom == nil || *m.SilentFrom != 2 {
		t.Fatalf("silent from %v, want second 2: warmup is second 0 of the numbering", m.SilentFrom)
	}
	if m.RPSLow != 40 || m.RPSHigh != 40 {
		t.Errorf("rates %d-%d, want 40-40: only the second stage is silent", m.RPSLow, m.RPSHigh)
	}
}

// Ground: contract — with no silent stretch the rates cover the whole plan.
func TestReport_RatesCoverThePlanWithoutSilence(t *testing.T) {
	eng, err := New(Options{
		Calls: []Call{{Method: "a", Timeout: 100 * time.Millisecond, Stages: []Stage{
			{StartRPS: 20, TargetRPS: 20, Duration: 500 * time.Millisecond},
			{StartRPS: 40, TargetRPS: 40, Duration: 500 * time.Millisecond},
		}}},
		Sender:      FakeSender{Delay: time.Millisecond},
		MaxInFlight: 100,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := eng.Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	m := eng.Report().Methods[0]
	if m.SilentFrom != nil {
		t.Errorf("silent from %d, want none: every call was answered", *m.SilentFrom)
	}
	if m.RPSLow != 20 || m.RPSHigh != 40 {
		t.Errorf("rates %d-%d, want 20-40", m.RPSLow, m.RPSHigh)
	}
	if got := eng.Report().Planned; got != time.Second {
		t.Errorf("planned %v, want 1s", got)
	}
}
