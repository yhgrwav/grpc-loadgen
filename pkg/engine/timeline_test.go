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

// Ground: boundary — a second an hour into a run; no end-to-end test runs an hour.
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

// Ground: boundary — an event exactly on a second boundary.
func TestTimeline_BoundaryBelongsToTheNextSecond(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	stats.Record(call(start, time.Second-time.Nanosecond, time.Second, CategorySuccess))

	got := seconds(t, stats)
	if got[0].Begun != 1 || got[0].Succeeded != 0 || got[1].Succeeded != 1 {
		t.Errorf("seconds = %+v, want begun in 0 and done in 1", got)
	}
}

// Ground: boundary — in flight at the exact end of each second.
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
// Ground: contract — Second outcomes, until a test/measure test reads Report.Timeline.
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

// Ground: contract — the seven outcomes of a Second.
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
		Begun: 10, Succeeded: 1, TargetFailed: 2, Overload: 1, Failure: 1, TimedOut: 1, RequestFailed: 1, NotSentConnection: 1, NotSentGenerator: 1,
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
	// A sender that could not prove the connection ready: the timing decides.
	r.NotSentOn = BlockedOnConnection

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
// Ground: contract — whose fault an outcome is.
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
// Ground: boundary — the budget-share split of unsent calls, to the millisecond.
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
	// The same timing, but the sender saw a ready connection with streams to
	// spare: its word outranks the timing.
	blamed := unsentAfterLag(start, 50*time.Millisecond, 450*time.Millisecond)
	blamed.NotSentOn = BlockedOnGenerator
	stats.Record(blamed)

	got := seconds(t, stats)[0]
	if got.NotSentConnection != 4 || got.NotSentGenerator != 8 || got.TargetFailed != 0 {
		t.Errorf("connection %d, generator %d, target failed %d; want 4, 8 and 0",
			got.NotSentConnection, got.NotSentGenerator, got.TargetFailed)
	}
}

// Ground: boundary — the zero Category, a sender that forgot to fill it in; no real sender does.
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

// Only successes carry a transport wait and a service time: for a refused
// connection SentAt means nothing, a timeout knows its service time only as a
// lower bound, and a fast refusal would pass for a faster target.
// Ground: contract — which calls enter the sums of a Second.
func TestTimeline_OnlySuccessesEnterTransportAndService(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for range 4 {
		stats.Record(decomposed(start, CategorySuccess, time.Millisecond, 2*time.Millisecond, 3*time.Millisecond))
	}
	for _, c := range []Category{
		CategoryClientFault, CategoryServerFault, CategoryOverload,
		CategoryUnreachable, CategoryAborted, CategoryTimeout, CategoryUnknown,
	} {
		stats.Record(decomposed(start, c, time.Millisecond, 20*time.Millisecond, 30*time.Millisecond))
	}
	stats.Record(unsent(start, 0, 50*time.Millisecond))

	got := seconds(t, stats)[0]
	if got.ObservedCalls != 4 || got.TransportWaitSum != 8*time.Millisecond || got.ServiceTimeSum != 12*time.Millisecond {
		t.Errorf("observed %d, transport %s, service %s; want 4, 8ms, 12ms",
			got.ObservedCalls, got.TransportWaitSum, got.ServiceTimeSum)
	}
	// The lag stays over every call begun, answered or not.
	if got.LagCalls != 12 || got.LagSum != 11*time.Millisecond || got.ObservedLagSum != 4*time.Millisecond {
		t.Errorf("lag calls %d, lag %s, observed lag %s; want 12, 11ms, 4ms",
			got.LagCalls, got.LagSum, got.ObservedLagSum)
	}
}

// A target shedding load refuses half the calls in 2ms and still serves the
// rest in 200ms. Averaged together that is 101ms: a target twice as fast under
// overload. Google's SRE book (Monitoring Distributed Systems, the four golden
// signals) warns of exactly this mix.
// Ground: contract — which calls enter the sums of a Second.
func TestTimeline_FastRefusalsDoNotMakeTheTargetLookFaster(t *testing.T) {
	start := time.Now()
	stats := reserved(start, 0)

	for range 100 {
		stats.Record(decomposed(start, CategorySuccess, 0, 0, 200*time.Millisecond))
		stats.Record(decomposed(start, CategoryOverload, 0, 0, 2*time.Millisecond))
		stats.Record(decomposed(start, CategoryClientFault, 0, 0, time.Millisecond))
	}

	got := seconds(t, stats)[0]
	if got.ObservedCalls != 100 || got.ServiceTimeSum != 100*200*time.Millisecond {
		t.Errorf("observed %d, service %s; want 100 and 20s", got.ObservedCalls, got.ServiceTimeSum)
	}
}

// The three sums over the observed calls of a second add up to their
// latencies with nothing left over.
// Ground: contract — the three terms of a Second add up to its latency.
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

// Ground: boundary — an invalid lag, which a real run produces only when a clock jumps.
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
// Ground: contract — which calls enter the lag of a Second.
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

// Ground: boundary — a negative lag, which a real run produces only when a clock jumps.
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

// Ground: contract — Options.Warmup, until the stand switches its delay by time instead of call
// number.
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

// Ground: boundary — a call just past the reserved span.
func TestTimeline_PastTheReservedSpanIsCountedAside(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Reserve(5 * time.Second)
	stats.Start(start, 0)

	stats.Record(call(start, 2900*time.Millisecond, 5900*time.Millisecond, CategoryTimeout))
	stats.Record(call(start, 90*time.Second, 91*time.Second, CategorySuccess))

	got := stats.Report().Methods[0]
	if got.Seconds[5].TimedOut != 1 {
		t.Errorf("second 5 = %+v, want the call inside the span counted", got.Seconds[5])
	}
	if len(got.Seconds) != 6 || got.OutsideTimeline != 1 {
		t.Errorf("seconds = %d, outside = %d; want 6 and the late call outside", len(got.Seconds), got.OutsideTimeline)
	}
}

// Ground: boundary — a timeline with no span reserved.
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

// Ground: boundary — an event before the start, which a real run produces only when a clock jumps.
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

// Ground: boundary — a call finished before it began, which a real run produces only when a clock
// jumps.
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
// Ground: hot path — the timeline is written on every call.
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

// Ground: concurrency — recording from many goroutines.
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

// Ground: boundary — the moment is finer than the timeline's second, and the
// statement about the target rests on it: a bucket number moves the start of
// the silence by up to a second and is read as a clock time anyway.
func TestStats_LastAnswerIsTheScheduledMomentOfTheLastAnsweredCall(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	answeredAt := func(d time.Duration, category Category) Result {
		at := start.Add(d)
		return Result{
			Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
			Outcome: Outcome{Category: category, SentAt: at, DoneAt: at.Add(time.Millisecond)},
		}
	}

	stats.Record(answeredAt(7500*time.Millisecond, CategorySuccess))
	stats.Record(answeredAt(8*time.Second, CategoryOverload)) // a refusal is an answer
	stats.Record(answeredAt(8200*time.Millisecond, CategoryTimeout))
	stats.Record(answeredAt(9*time.Second, CategoryTimeout))
	stats.Finish(start.Add(10 * time.Second))

	got := stats.Report().Methods[0].LastAnswerAt
	if got == nil {
		t.Fatal("no last answer recorded")
	}
	if *got != 8*time.Second {
		t.Errorf("last answer at %v, want 8s: the last call the target answered", *got)
	}
}

// Ground: contract — any status the target sent back, a refusal or a rejected
// request included, shows it alive; a call it never answered does not.
func TestStats_LastAnswerMovesOnlyOnAStatusFromTheTarget(t *testing.T) {
	for _, tc := range []struct {
		category Category
		moves    bool
	}{
		{CategorySuccess, true},
		{CategoryServerFault, true},
		{CategoryOverload, true},
		{CategoryClientFault, true},
		{CategoryUnreachable, false},
		{CategoryTimeout, false},
		{CategoryAborted, false},
	} {
		t.Run(tc.category.String(), func(t *testing.T) {
			stats := NewStats()
			start := time.Now()
			stats.Start(start, 0)

			record := func(d time.Duration, category Category) {
				at := start.Add(d)
				stats.Record(Result{
					Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
					Outcome: Outcome{Category: category, SentAt: at, DoneAt: at.Add(time.Millisecond)},
				})
			}
			record(time.Second, CategorySuccess)
			record(5*time.Second, tc.category)
			stats.Finish(start.Add(10 * time.Second))

			want := time.Second
			if tc.moves {
				want = 5 * time.Second
			}
			got := stats.Report().Methods[0].LastAnswerAt
			if got == nil || *got != want {
				t.Errorf("last answer at %v, want %v", got, want)
			}
		})
	}
}

// Ground: boundary — a target that never answered has no such moment, and the
// zero of a duration would read as "answered at the start".
func TestStats_ATargetThatNeverAnsweredHasNoLastAnswer(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start.Add(time.Second)
	stats.Record(Result{
		Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryTimeout, SentAt: at, DoneAt: at.Add(time.Second)},
	})
	stats.Finish(start.Add(3 * time.Second))

	if got := stats.Report().Methods[0].LastAnswerAt; got != nil {
		t.Errorf("last answer at %v, want none", *got)
	}
}

// Ground: boundary — a timeout of a call that never went out is the
// generator's, and the statement "the target answered nothing from here on"
// must not rest on it. End to end this needs a generator starved on purpose.
func TestTimeline_ATimeoutThatNeverWentOutIsNotTheTargetsSilence(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Reserve(10*time.Second, "a")
	stats.Start(start, 0)

	answered := start.Add(time.Second)
	stats.Record(Result{
		Method: "a", ScheduledAt: answered, BegunAt: answered, Deadline: answered.Add(time.Second),
		Outcome: Outcome{Category: CategorySuccess, SentAt: answered, DoneAt: answered.Add(time.Millisecond)},
	})

	for i := range 3 {
		at := start.Add(time.Duration(2+i) * time.Second)
		stats.Record(Result{
			Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
			Outcome: Outcome{Category: CategoryTimeout, NotSent: true, SentAt: at, DoneAt: at.Add(time.Second)},
		})
	}
	stats.Finish(start.Add(6 * time.Second))

	m := stats.Report().Methods[0]
	if m.SilentFrom != nil {
		t.Errorf("silent from second %d, yet every timeout was the generator's own", *m.SilentFrom)
	}
	if m.UnsentTimedOut != 3 {
		t.Errorf("unsent timeouts = %d, want 3", m.UnsentTimedOut)
	}
}
