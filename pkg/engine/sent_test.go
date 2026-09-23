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

// 150 calls after warmup: 146 went out and got no answer, 4 timed out before
// going out. The target could have seen 146.
func silentWithUnsent(t *testing.T) *Stats {
	t.Helper()

	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)
	stats.EndSending(start.Add(2 * time.Second))

	for i := range 150 {
		at := start.Add(time.Duration(i) * 10 * time.Millisecond)
		stats.Record(Result{
			Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
			Outcome: Outcome{Category: CategoryTimeout, NotSent: i < 4, SentAt: at, DoneAt: at.Add(time.Second)},
		})
	}
	stats.Finish(start.Add(3 * time.Second))

	return stats
}

// Ground: contract — "sent" and sent/s count calls that went out to the
// target; a call that timed out before going out never reached it, and
// counting it inflates what the generator drove and dilutes the share the
// target did not answer ("146 of 150" reads as if it answered 4).
func TestStats_SentCountsOnlyCallsThatWentOut(t *testing.T) {
	report := silentWithUnsent(t).Report()
	m := report.Methods[0]

	if report.Sent != 146 || m.Sent != 146 {
		t.Errorf("sent: run %d, method %d; want 146: 4 calls never went out", report.Sent, m.Sent)
	}
	if m.UnsentTimedOut != 4 || m.TimedOut != 146 {
		t.Errorf("timed out %d, unsent %d; want 146 and 4", m.TimedOut, m.UnsentTimedOut)
	}
	// Sending ended at 2s: 146 went out over it.
	if m.RPS != 73 {
		t.Errorf("sent/s = %v, want 73: 146 over the 2s window", m.RPS)
	}
}

// Ground: contract — the live view's counters are the same numbers as the
// report's, so they obey the same rule.
func TestStats_LiveSentCountsOnlyCallsThatWentOut(t *testing.T) {
	snap := silentWithUnsent(t).Snapshot()

	if snap.Sent != 146 || snap.Methods[0].Sent != 146 {
		t.Errorf("live sent: run %d, method %d; want 146", snap.Sent, snap.Methods[0].Sent)
	}
	if snap.RPS != 73 || snap.Methods[0].RPS != 73 {
		t.Errorf("live sent/s: run %v, method %v; want 73", snap.RPS, snap.Methods[0].RPS)
	}
}

// Ground: contract — every call after warmup either went out or did not;
// "not sent" is its own count so the two add up to what was scheduled.
func TestStats_SentAndNotSentAddUpToEveryCall(t *testing.T) {
	stats := silentWithUnsent(t)
	report, snap := stats.Report(), stats.Snapshot()

	if report.Sent+report.NotSent != 150 || report.NotSent != 4 {
		t.Errorf("report: sent %d + not sent %d; want 146 + 4 = 150", report.Sent, report.NotSent)
	}
	if snap.Sent+snap.NotSent != 150 || snap.NotSent != 4 {
		t.Errorf("live: sent %d + not sent %d; want 146 + 4 = 150", snap.Sent, snap.NotSent)
	}
}

// Ground: contract — "failed" is a number about the target; a call that never
// went out is not its failure. Failed over sent then stays within 100%.
func TestStats_FailedCountsOnlyCallsThatWentOut(t *testing.T) {
	stats := silentWithUnsent(t)
	report, snap := stats.Report(), stats.Snapshot()

	if report.Failed != 146 || report.Methods[0].Failed != 146 {
		t.Errorf("failed: run %d, method %d; want 146", report.Failed, report.Methods[0].Failed)
	}
	if snap.Failed != 146 || snap.Methods[0].Failed != 146 {
		t.Errorf("live failed: run %d, method %d; want 146", snap.Failed, snap.Methods[0].Failed)
	}
}

// Ground: contract — a censored observation is a lower bound on how long the
// target took; a call that never went out gives no such bound. Were the 4 in
// the distribution, 4 of the 150 would be ">1s" bounds on nothing.
func TestStats_UnsentCallsStayOutOfTheLatencies(t *testing.T) {
	m := silentWithUnsent(t).Report().Methods[0]

	if m.Censored != 146 || m.Latencies != 146 {
		t.Errorf("censored %d of %d observations; want 146 of 146", m.Censored, m.Latencies)
	}
}

// Ground: contract — an unreachable target is the target's state: the call
// went out and failed, so it is sent and failed, and not "not sent".
func TestStats_UnreachableCallsWentOut(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)
	for i := range 10 {
		at := start.Add(time.Duration(i) * time.Millisecond)
		stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at,
			Outcome: Outcome{Category: CategoryUnreachable, SentAt: at, DoneAt: at}})
	}
	stats.Finish(start.Add(time.Second))
	report := stats.Report()

	if report.Sent != 10 || report.Failed != 10 || report.NotSent != 0 {
		t.Errorf("sent %d, failed %d, not sent %d; want 10, 10, 0", report.Sent, report.Failed, report.NotSent)
	}
}

// Ground: contract — the verdict says every call the target saw was rejected.
// Calls that never went out are not calls it saw: 10 rejected and 4 unsent is
// still "every call rejected"; 4 unsent and nothing else is no verdict.
func TestStats_RequestRejectedCountsOnlyCallsThatWentOut(t *testing.T) {
	record := func(stats *Stats, start time.Time, method string, rejected, unsent int) {
		for i := range rejected + unsent {
			at := start.Add(time.Duration(i) * time.Millisecond)
			out := Outcome{Category: CategoryClientFault, SentAt: at, DoneAt: at}
			if i < unsent {
				out = Outcome{Category: CategoryTimeout, NotSent: true, SentAt: at, DoneAt: at.Add(time.Second)}
			}
			stats.Record(Result{Method: method, ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second), Outcome: out})
		}
	}

	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)
	record(stats, start, "a", 10, 4)
	stats.Finish(start.Add(time.Second))
	if !stats.Report().RequestRejected {
		t.Error("10 rejected and 4 unsent: want the verdict, every call that went out was rejected")
	}

	stats = NewStats()
	stats.Start(start, 0)
	record(stats, start, "a", 0, 4)
	stats.Finish(start.Add(time.Second))
	if stats.Report().RequestRejected {
		t.Error("4 unsent and nothing sent: want no verdict, the target saw nothing")
	}
}

// stuckSender holds every call past its deadline and the release margin, so
// the cap fills on the generator's side; every third call never went out.
type stuckSender struct{ sends atomic.Int64 }

func (s *stuckSender) Send(ctx context.Context, _ Request) (Outcome, error) {
	i := s.sends.Add(1)
	time.Sleep(500 * time.Millisecond) // the hold under test, not synchronisation

	return Outcome{Category: CategoryTimeout, NotSent: i%3 == 0, Err: context.DeadlineExceeded, DoneAt: time.Now()}, nil
}

// Ground: contract — the report accounts for every call the schedule handed
// out: sent, not sent, and the one the cap refused, which it prints as
// CapHit.Unsent. A call counted twice or in none breaks the sum.
func TestEngine_SentNotSentAndCapRefusedAddUpToEveryCall(t *testing.T) {
	sender := &stuckSender{}
	// 100/s with a 100ms timeout needs 10 + 1 + 10 slots; the sender holds
	// each for 500ms, so the cap of 21 is hit at about 210ms.
	eng, err := New(Options{Calls: []Call{budgetCall("a", 100, 100*time.Millisecond)}, Sender: sender, MaxInFlight: 21})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Run(t.Context()); !errors.Is(err, ErrInFlightCapExceeded) {
		t.Fatalf("run: %v, want the cap hit", err)
	}

	report := eng.Report()
	if report.CapHit == nil {
		t.Fatal("no cap hit in the report")
	}
	sends := int(sender.sends.Load())
	if got := report.Sent + report.NotSent; got != sends {
		t.Errorf("sent %d + not sent %d = %d; the sender was handed %d", report.Sent, report.NotSent, got, sends)
	}
	if report.NotSent != sends/3 || report.CapHit.Unsent != 1 {
		t.Errorf("not sent %d, refused by the cap %d; want %d and 1", report.NotSent, report.CapHit.Unsent, sends/3)
	}
}

// Ground: contract — start lag and late cancellation are the generator's
// numbers, and a call that timed out before going out is most often the one
// the generator was late with: leaving it out hides the lag that caused it.
func TestStats_UnsentCallsStillCountForTheGeneratorsLag(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)
	at := start.Add(10 * time.Millisecond)
	stats.Record(Result{
		Method: "a", ScheduledAt: at, BegunAt: at.Add(300 * time.Millisecond), Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryTimeout, NotSent: true, DoneAt: at.Add(time.Second + 50*time.Millisecond)},
	})
	stats.Finish(start.Add(2 * time.Second))
	report := stats.Report()

	if report.StartLagMax != 300*time.Millisecond {
		t.Errorf("start lag max %v, want 300ms from the unsent call", report.StartLagMax)
	}
	if report.LateCancelMax != 50*time.Millisecond {
		t.Errorf("late cancel max %v, want 50ms from the unsent call", report.LateCancelMax)
	}
}
