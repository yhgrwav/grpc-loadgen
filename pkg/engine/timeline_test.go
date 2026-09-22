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
	"sync"
	"testing"
	"time"
)

// call builds a result scheduled and begun at begun, finished at done, all as
// offsets from start.
func call(start time.Time, begun, done time.Duration, category Category) Result {
	return Result{
		Method:      "a",
		ScheduledAt: start.Add(begun),
		BegunAt:     start.Add(begun),
		Outcome:     Outcome{SentAt: start.Add(begun), DoneAt: start.Add(done), Category: category},
	}
}

// reserved is a Stats with a timeline of an hour, started at start.
func reserved(start time.Time, warmup time.Duration) *Stats {
	stats := NewStats()
	stats.Reserve(time.Hour)
	stats.Start(start, warmup)

	return stats
}

func seconds(t *testing.T, stats *Stats) []Second {
	t.Helper()

	report := stats.Report()
	if len(report.Methods) != 1 {
		t.Fatalf("methods = %d, want 1", len(report.Methods))
	}

	return report.Methods[0].Seconds
}

func TestTimeline_SecondFarIntoALongRunIsItsOwnWindow(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, 3590*time.Second+500*time.Millisecond, 3591*time.Second+100*time.Millisecond, CategorySuccess))

	got := seconds(t, stats)
	if len(got) != 3592 {
		t.Fatalf("seconds = %d, want 3592", len(got))
	}
	if got[3590].Begun != 1 || got[3591].Succeeded != 1 {
		t.Errorf("second 3590 = %+v, 3591 = %+v; want begun in 3590, done in 3591", got[3590], got[3591])
	}
}

func TestTimeline_BoundaryBelongsToTheNextSecond(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, time.Second-time.Nanosecond, time.Second, CategorySuccess))

	got := seconds(t, stats)
	if got[0].Begun != 1 || got[0].Succeeded != 0 || got[1].Succeeded != 1 {
		t.Errorf("seconds = %+v, want begun in 0 and done in 1", got)
	}
}

func TestTimeline_InFlightAtTheEndOfEachSecond(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, 2500*time.Millisecond, 5200*time.Millisecond, CategorySuccess))

	got := seconds(t, stats)
	want := []int{0, 0, 1, 1, 1, 0}
	if len(got) != len(want) {
		t.Fatalf("seconds = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].InFlight != w {
			t.Errorf("second %d in flight = %d, want %d", i, got[i].InFlight, w)
		}
	}
}

// A target shedding load answers every call with a refusal in two
// milliseconds: nothing piles up in flight and the generator keeps pace, so
// only the split of finished calls by outcome shows it serves nothing.
func TestTimeline_FastRefusalsAreFailuresNotServedCalls(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for i := range 3000 {
		begun := time.Duration(i) * time.Millisecond
		stats.Record(call(start, begun, begun+2*time.Millisecond, CategoryOverload))
	}

	got := seconds(t, stats)
	for i := range 2 {
		if got[i].Succeeded != 0 || got[i].TargetFailed < 998 {
			t.Errorf("second %d = %+v, want only failures", i, got[i])
		}
		if got[i].InFlight > 2 {
			t.Errorf("second %d in flight = %d, want at most 2", i, got[i].InFlight)
		}
	}
}

func TestTimeline_OutcomesAreSplit(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for _, c := range []Category{
		CategorySuccess, CategoryOverload, CategoryTimeout, CategoryServerFault, CategoryClientFault,
		CategoryUnreachable, CategoryUnknown, CategoryAborted,
	} {
		stats.Record(call(start, 0, time.Millisecond, c))
	}
	stats.Record(unsent(start, 0, time.Millisecond))
	stats.Record(unsent(start, 0, -time.Millisecond))

	got := seconds(t, stats)[0]
	want := Second{
		Begun: 10, Succeeded: 1, TargetFailed: 3, RequestFailed: 1, UnsentQuota: 1, UnsentLate: 1,
		Unanswered: 1, Aborted: 1, Unclassified: 1,
	}
	got.LagSum, got.LagMax, got.LagCalls = 0, 0, 0
	got.ObservedCalls, got.ObservedLagSum, got.TransportWaitSum, got.ServiceTimeSum = 0, 0, 0, 0
	// Every outcome ends a call, so nothing is left in flight.
	if got != want {
		t.Errorf("second 0 = %+v, want %+v", got, want)
	}
}

// unsent is a timeout whose request never went out. Its deadline is begun+
// untilDeadline: past the call's start when the call waited for the
// connection, before it when the generator started it too late to go out.
func unsent(start time.Time, begun, untilDeadline time.Duration) Result {
	r := call(start, begun, begun+max(untilDeadline, 0), CategoryTimeout)
	r.Deadline = r.BegunAt.Add(untilDeadline)
	r.SentAt = r.DoneAt
	r.NotSent = true

	return r
}

// unsentAfterLag is a timeout scheduled at the start, begun lag later, that
// then waited on the connection until its deadline.
func unsentAfterLag(start time.Time, lag, wait time.Duration) Result {
	r := unsent(start, lag, wait)
	r.ScheduledAt = start

	return r
}

// A config that sends the same entity every time gets AlreadyExists from the
// second call on. The target copes fine; the verdict reads TargetFailed, so
// these must not land there.
func TestTimeline_RequestFaultsAreNotTheTargets(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for i := range 100 {
		begun := time.Duration(i) * time.Millisecond
		stats.Record(call(start, begun, begun+time.Millisecond, CategoryClientFault))
	}

	got := seconds(t, stats)[0]
	if got.RequestFailed != 100 || got.TargetFailed != 0 {
		t.Errorf("request failed %d, target failed %d; want 100 and 0", got.RequestFailed, got.TargetFailed)
	}
}

// A request that never went out tells two different stories: stuck behind
// the connection's stream quota, or started by a generator already past the
// deadline. Opening more connections helps only the first.
func TestTimeline_UnsentSplitsByWhoseFault(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for range 3 {
		stats.Record(unsent(start, 0, 100*time.Millisecond))
	}
	for range 5 {
		stats.Record(unsent(start, 0, -time.Millisecond))
	}
	// Started exactly at the deadline: no budget left, the generator's doing.
	stats.Record(unsent(start, 0, 0))

	// Half a second of budget. The generator ate 450ms of it, the quota the
	// last 50ms: the generator's doing, though the call began before the deadline.
	stats.Record(unsentAfterLag(start, 450*time.Millisecond, 50*time.Millisecond))
	// And the other way round: the connection ate 450ms of it.
	stats.Record(unsentAfterLag(start, 50*time.Millisecond, 450*time.Millisecond))

	got := seconds(t, stats)[0]
	if got.UnsentQuota != 4 || got.UnsentLate != 7 || got.TargetFailed != 0 {
		t.Errorf("quota %d, late %d, target failed %d; want 4, 7 and 0",
			got.UnsentQuota, got.UnsentLate, got.TargetFailed)
	}
}

func TestTimeline_UnknownIsUnclassifiedNotUnanswered(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, 0, time.Millisecond, CategoryUnknown))
	stats.Record(call(start, 0, time.Millisecond, CategoryUnreachable))

	m := stats.Report().Methods[0]
	if s := m.Seconds[0]; s.Unclassified != 1 || s.Unanswered != 1 {
		t.Errorf("second: unclassified %d, unanswered %d; want 1 and 1", s.Unclassified, s.Unanswered)
	}
	if m.Unclassified != 1 || m.Unanswered != 1 {
		t.Errorf("method: unclassified %d, unanswered %d; want 1 and 1", m.Unclassified, m.Unanswered)
	}
}

// decomposed is a call with each latency term set apart, all in the second
// the call was scheduled for.
func decomposed(start time.Time, category Category, lag, wait, service time.Duration) Result {
	r := call(start, 0, 0, category)
	r.BegunAt = r.ScheduledAt.Add(lag)
	r.SentAt = r.BegunAt.Add(wait)
	r.DoneAt = r.SentAt.Add(service)

	return r
}

// Only fully observed calls carry a transport wait and a service time: for a
// refused connection SentAt means nothing, and a timeout knows its service
// time only as a lower bound.
func TestTimeline_OnlyObservedCallsEnterTransportAndService(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for _, c := range []Category{CategorySuccess, CategoryClientFault, CategoryServerFault, CategoryOverload} {
		stats.Record(decomposed(start, c, time.Millisecond, 2*time.Millisecond, 3*time.Millisecond))
	}
	for _, c := range []Category{CategoryUnreachable, CategoryAborted, CategoryTimeout, CategoryUnknown} {
		stats.Record(decomposed(start, c, time.Millisecond, 20*time.Millisecond, 30*time.Millisecond))
	}
	stats.Record(unsent(start, 0, 50*time.Millisecond))

	got := seconds(t, stats)[0]
	if got.ObservedCalls != 4 || got.TransportWaitSum != 8*time.Millisecond || got.ServiceTimeSum != 12*time.Millisecond {
		t.Errorf("observed %d, transport %s, service %s; want 4, 8ms, 12ms",
			got.ObservedCalls, got.TransportWaitSum, got.ServiceTimeSum)
	}
	// The lag stays over every call begun, answered or not.
	if got.LagCalls != 9 || got.LagSum != 8*time.Millisecond || got.ObservedLagSum != 4*time.Millisecond {
		t.Errorf("lag calls %d, lag %s, observed lag %s; want 9, 8ms, 4ms",
			got.LagCalls, got.LagSum, got.ObservedLagSum)
	}
}

// The three sums over the observed calls of a second add up to their
// latencies with nothing left over.
func TestTimeline_ObservedTermsAddUpToLatency(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	var latencies time.Duration
	for i := range 500 {
		d := time.Duration(i)
		r := decomposed(start, CategorySuccess, d*time.Microsecond, d*3*time.Microsecond, d*7*time.Microsecond)
		r.ScheduledAt = r.ScheduledAt.Add(d * time.Millisecond)
		r.BegunAt = r.BegunAt.Add(d * time.Millisecond)
		r.SentAt = r.SentAt.Add(d * time.Millisecond)
		r.DoneAt = r.DoneAt.Add(d * time.Millisecond)
		latencies += r.Latency()
		stats.Record(r)
	}

	got := seconds(t, stats)[0]
	if sum := got.ObservedLagSum + got.TransportWaitSum + got.ServiceTimeSum; sum != latencies {
		t.Errorf("lag + transport + service = %s, want %s", sum, latencies)
	}
}

func TestTimeline_InvalidLagIsNotALagCall(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	r := call(start, 100*time.Millisecond, 200*time.Millisecond, CategorySuccess)
	r.BegunAt = r.ScheduledAt.Add(-time.Millisecond)
	stats.Record(r)
	stats.Record(call(start, 100*time.Millisecond, 200*time.Millisecond, CategorySuccess))

	if got := seconds(t, stats)[0]; got.LagCalls != 1 || got.ObservedCalls != 1 {
		t.Errorf("lag calls %d, observed %d; want 1 and 1", got.LagCalls, got.ObservedCalls)
	}
}

// The generator's lag is taken before the call and says nothing about the
// reply, so a refused connection or an aborted call is lag like any other.
func TestTimeline_UnansweredAndAbortedCountInLag(t *testing.T) {
	lagOf := func(category Category) MethodReport {
		start := time.Now()
		stats := reserved(start, 0)

		for i := range 1000 {
			r := call(start, 0, time.Millisecond, category)
			// Falling lag, so the maximum is the first one, not the last.
			r.BegunAt = r.ScheduledAt.Add(time.Duration(999-i) * time.Microsecond)
			stats.Record(r)
		}

		seconds(t, stats)

		return stats.Report().Methods[0]
	}

	success := lagOf(CategorySuccess)
	if s := success.Seconds[0]; s.LagMax != 999*time.Microsecond || s.LagSum != 499500*time.Microsecond {
		t.Fatalf("success lag = %s max %s, want 499.5ms max 999µs", s.LagSum, s.LagMax)
	}
	// A call begun exactly on schedule has zero lag, which is a fact, not an error.
	if success.InvalidLag != 0 {
		t.Errorf("invalid lag = %d, want 0", success.InvalidLag)
	}

	for _, c := range []Category{CategoryUnreachable, CategoryAborted} {
		got := lagOf(c).Seconds[0]
		want := success.Seconds[0]
		if got.LagSum != want.LagSum || got.LagMax != want.LagMax {
			t.Errorf("%s lag = %s max %s, want the same as success", c, got.LagSum, got.LagMax)
		}
	}
}

func TestTimeline_NegativeLagIsInvalidNotZero(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	r := call(start, 100*time.Millisecond, 200*time.Millisecond, CategorySuccess)
	r.BegunAt = r.ScheduledAt.Add(-time.Millisecond)
	stats.Record(r)

	report := stats.Report()
	got := report.Methods[0]
	if got.Seconds[0].LagSum != 0 || got.Seconds[0].LagMax != 0 || got.InvalidLag != 1 {
		t.Errorf("lag = %s max %s, invalid %d; want nothing recorded and one invalid",
			got.Seconds[0].LagSum, got.Seconds[0].LagMax, got.InvalidLag)
	}
}

func TestTimeline_WarmupIsOnTheTimelineButNotInTheTotals(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 2*time.Second)

	stats.Record(call(start, 500*time.Millisecond, 600*time.Millisecond, CategoryServerFault))
	stats.Record(call(start, 2500*time.Millisecond, 2600*time.Millisecond, CategorySuccess))

	report := stats.Report()
	if report.Warmup != 2*time.Second {
		t.Errorf("warmup = %s, want 2s", report.Warmup)
	}
	if report.Sent != 1 || report.Failed != 0 {
		t.Errorf("totals sent %d failed %d, want 1 and 0: warmup stays out", report.Sent, report.Failed)
	}

	got := report.Methods[0].Seconds
	if got[0].TargetFailed != 1 || got[2].Succeeded != 1 {
		t.Errorf("seconds = %+v, want the warmup failure in 0 and the success in 2", got)
	}
}

func TestTimeline_PastTheReservedSpanIsCountedAside(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Reserve(5 * time.Second)
	stats.Start(start, 0)

	stats.Record(call(start, 2900*time.Millisecond, 5900*time.Millisecond, CategoryTimeout))
	stats.Record(call(start, 90*time.Second, 91*time.Second, CategorySuccess))

	got := stats.Report().Methods[0]
	if got.Seconds[5].TargetFailed != 1 {
		t.Errorf("second 5 = %+v, want the call inside the span counted", got.Seconds[5])
	}
	if len(got.Seconds) != 6 || got.OutsideTimeline != 1 {
		t.Errorf("seconds = %d, outside = %d; want 6 and the late call outside", len(got.Seconds), got.OutsideTimeline)
	}
}

func TestTimeline_WithoutReserveEverythingIsOutside(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(call(start, 0, time.Millisecond, CategorySuccess))

	got := stats.Report().Methods[0]
	if len(got.Seconds) != 0 || got.OutsideTimeline != 1 {
		t.Errorf("seconds = %d, outside = %d; want none and one", len(got.Seconds), got.OutsideTimeline)
	}
}

func TestTimeline_EventBeforeTheStartIsCountedAside(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, -time.Second, time.Second, CategorySuccess))

	got := stats.Report().Methods[0]
	if got.OutsideTimeline != 1 {
		t.Errorf("outside = %d, want 1", got.OutsideTimeline)
	}
	// Counting only its end would leave in flight at minus one from then on.
	for i, s := range got.Seconds {
		if s != (Second{}) {
			t.Errorf("second %d = %+v, want the call left off the timeline whole", i, s)
		}
	}
}

func TestTimeline_FinishedBeforeBegunIsCountedAside(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	r := call(start, 2500*time.Millisecond, 3*time.Second, CategorySuccess)
	r.DoneAt = start.Add(1500 * time.Millisecond)
	stats.Record(r)

	got := stats.Report().Methods[0]
	if got.OutsideTimeline != 1 {
		t.Errorf("outside = %d, want 1", got.OutsideTimeline)
	}
	for i, s := range got.Seconds {
		if s != (Second{}) {
			t.Errorf("second %d = %+v, want the call left off whole", i, s)
		}
	}
}

// CI runs no benchmarks, so an allocation creeping into recording would go
// unnoticed without this.
func TestTimeline_RecordDoesNotAllocate(t *testing.T) {
	start := time.Now()
	stats := NewStats()
	stats.Reserve(time.Hour, "a")
	stats.Start(start, 0)

	r := call(start, 500*time.Millisecond, 600*time.Millisecond, CategorySuccess)

	if allocs := testing.AllocsPerRun(1000, func() { stats.Record(r) }); allocs != 0 {
		t.Errorf("Record allocates %v times per call, want 0", allocs)
	}
}

func TestTimeline_ConcurrentRecordingLosesNothing(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	const writers, each = 50, 200

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range each {
				begun := time.Duration(w*each+i) * time.Millisecond
				stats.Record(call(start, begun, begun+time.Millisecond, CategorySuccess))
			}
		}()
	}
	wg.Wait()

	var begun, succeeded int
	for _, s := range seconds(t, stats) {
		begun += s.Begun
		succeeded += s.Succeeded
	}
	if begun != writers*each || succeeded != writers*each {
		t.Errorf("begun %d, succeeded %d; want %d each", begun, succeeded, writers*each)
	}
}
