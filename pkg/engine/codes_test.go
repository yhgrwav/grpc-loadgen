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
	"slices"
	"testing"
	"time"
)

// failures records, after a 1s warmup, the calls of method a: ok successes,
// and per code the given number of failures in category c.
func failures(t *testing.T, ok int, byCode map[string]int, c Category) *Stats {
	t.Helper()

	stats := NewStats()
	start := time.Now()
	stats.Start(start, time.Second)

	at := start.Add(2 * time.Second)
	rec := func(o Outcome) {
		o.SentAt, o.DoneAt = at, at.Add(time.Millisecond)
		stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second), Outcome: o})
		at = at.Add(time.Millisecond)
	}

	for range ok {
		rec(Outcome{Category: CategorySuccess, Code: "OK"})
	}
	for code, n := range byCode {
		for range n {
			rec(Outcome{Category: c, Code: code})
		}
	}

	// Inside the warmup and never sent: neither is a failure the report counts.
	stats.Record(Result{Method: "a", ScheduledAt: start, BegunAt: start,
		Outcome: Outcome{Category: c, Code: "Warmup", SentAt: start, DoneAt: start}})
	stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryTimeout, Code: "NotSent", NotSent: true, SentAt: at, DoneAt: at.Add(time.Second)}})

	stats.EndSending(at)
	stats.Finish(at.Add(time.Second))

	return stats
}

// Ground: contract — MethodReport.FailureCodes is public: every failed call is under its
// transport code, the commonest first, a tie by name; successes, warmup and unsent calls are not.
func TestReport_FailureCodesCountEveryFailedCall(t *testing.T) {
	stats := failures(t, 10, map[string]int{"Unavailable": 5, "Internal": 2, "Aborted": 2}, CategoryOverload)
	m := stats.Report().Methods[0]

	want := []CodeCount{{"Unavailable", 5, false}, {"Aborted", 2, false}, {"Internal", 2, false}}
	if !slices.Equal(m.FailureCodes, want) {
		t.Errorf("failure codes %v, want %v", m.FailureCodes, want)
	}

	total := 0
	for _, c := range m.FailureCodes {
		total += c.Count
	}
	if total != m.Failed {
		t.Errorf("codes add up to %d, the method failed %d", total, m.Failed)
	}
}

// Ground: contract — nothing failed, nothing listed: nil, not an empty row to print.
func TestReport_NoFailuresNoCodes(t *testing.T) {
	m := failures(t, 10, nil, CategoryOverload).Report().Methods[0]

	if m.FailureCodes != nil {
		t.Errorf("failure codes %v, want nil", m.FailureCodes)
	}
}

// Ground: contract — a timeout and a call that never reached the target are failures too; the code
// tells a deadline from a refused connection.
func TestReport_FailureCodesIncludeTimeoutsAndUnreachable(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start
	stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryTimeout, Code: "DeadlineExceeded", SentAt: at, DoneAt: at.Add(time.Second)}})
	stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryUnreachable, Code: "Unavailable", SentAt: at, DoneAt: at}})
	stats.EndSending(at)
	stats.Finish(at.Add(time.Second))

	want := []CodeCount{{"DeadlineExceeded", 1, false}, {"Unavailable", 1, false}}
	if got := stats.Report().Methods[0].FailureCodes; !slices.Equal(got, want) {
		t.Errorf("failure codes %v, want %v", got, want)
	}
}

// The same code from two sources is two entries: one the target sent, one the
// client made. Summed together they would put the client's timeouts on the target.
//
// Ground: contract — engine.CodeCount.FromTarget is public.
func TestReport_FailureCodesKeepWhoMadeTheCode(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start
	rec := func(code string, fromTarget bool, n int) {
		for range n {
			stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
				Outcome: Outcome{Category: CategoryTimeout, Code: code, CodeFromTarget: fromTarget,
					SentAt: at, DoneAt: at.Add(time.Second)}})
			at = at.Add(time.Millisecond)
		}
	}
	rec("DeadlineExceeded", false, 5)
	rec("DeadlineExceeded", true, 2)
	rec("Unavailable", true, 3)

	stats.EndSending(at)
	stats.Finish(at.Add(time.Second))

	want := []CodeCount{
		{Code: "Unavailable", Count: 3, FromTarget: true},
		{Code: "DeadlineExceeded", Count: 2, FromTarget: true},
		{Code: "DeadlineExceeded", Count: 5, FromTarget: false},
	}
	if got := stats.Report().Methods[0].FailureCodes; !slices.Equal(got, want) {
		t.Errorf("FailureCodes = %+v\nwant %+v: the target's codes first, then the client's, each by count", got, want)
	}
}

// Every failed call with a code is in exactly one group: none lost between the
// two, none counted in both.
//
// Ground: contract — MethodReport.FailureCodes is public.
func TestReport_FailureCodesOfBothGroupsSumToTheFailures(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	names := []string{"Unavailable", "Internal", "DeadlineExceeded"}
	methods := []string{"a", "b"}
	withCode := map[string]int{}

	at := start
	for i := range 60 {
		m := methods[i%2]
		stats.Record(Result{Method: m, ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
			Outcome: Outcome{Category: CategoryOverload, Code: names[i%3], CodeFromTarget: i%4 < 2,
				SentAt: at, DoneAt: at.Add(time.Millisecond)}})
		withCode[m]++
		at = at.Add(time.Millisecond)
	}
	stats.EndSending(at)
	stats.Finish(at.Add(time.Second))

	for _, m := range stats.Report().Methods {
		sum, target := 0, 0
		for _, c := range m.FailureCodes {
			sum += c.Count
			if c.FromTarget {
				target += c.Count
			}
		}
		if sum != withCode[m.Method] || sum != m.Failed {
			t.Errorf("%s: codes sum to %d, %d failed with a code, Failed %d", m.Method, sum, withCode[m.Method], m.Failed)
		}
		if target == 0 || target == sum {
			t.Errorf("%s: %d of %d in the target's group, want both groups filled: %+v", m.Method, target, sum, m.FailureCodes)
		}
	}
}

// Calls cancelled by stopping the run are abandoned, not failed: their Canceled
// is in no codes line.
//
// Ground: contract — MethodReport.FailureCodes is public.
func TestReport_CallsCancelledByTheStopHaveNoCode(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start
	stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryAborted, Code: "Canceled", SentAt: at, DoneAt: at.Add(time.Millisecond)}})
	stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{Category: CategoryOverload, Code: "Unavailable", CodeFromTarget: true, SentAt: at, DoneAt: at.Add(time.Millisecond)}})
	stats.EndSending(at)
	stats.Finish(at.Add(time.Second))

	got := stats.Report().Methods[0].FailureCodes
	if want := []CodeCount{{Code: "Unavailable", Count: 1, FromTarget: true}}; !slices.Equal(got, want) {
		t.Errorf("FailureCodes = %+v, want %+v: a call cancelled by the stop has no code", got, want)
	}
}

// A cut-off call went out and got no status: a failure with no latency to
// speak of, counted apart from both the service time and the error statuses,
// and never under the target's codes.
//
// Ground: contract — MethodReport.CutOff is public.
func TestReport_CutOffIsAFailureOfItsOwn(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start
	for range 4 {
		stats.Record(Result{Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
			Outcome: Outcome{Category: CategoryCutOff, Code: "Internal", SentAt: at, DoneAt: at.Add(3 * time.Millisecond)}})
		at = at.Add(time.Millisecond)
	}
	stats.EndSending(at)
	stats.Finish(at.Add(time.Second))

	m := stats.Report().Methods[0]
	if m.Failed != 4 || m.CutOff != 4 {
		t.Errorf("failed %d, cut off %d, want 4 and 4", m.Failed, m.CutOff)
	}
	if m.Unanswered != 0 {
		t.Errorf("unanswered %d: a cut-off call reached the other end", m.Unanswered)
	}
	if m.Overload.Count+m.Failure.Count != 0 || m.Rejected.Count != 0 {
		t.Errorf("overload %d, failure %d, rejected %d: no status came back to time", m.Overload.Count, m.Failure.Count, m.Rejected.Count)
	}
	if want := []CodeCount{{Code: "Internal", Count: 4}}; !slices.Equal(m.FailureCodes, want) {
		t.Errorf("codes %+v, want %+v", m.FailureCodes, want)
	}
}

// A cut-off call is no answer: the last answer stays at the last call whose
// status came back, and the second counts it apart from the target's statuses.
//
// Ground: contract — MethodReport.LastAnswerAt and Second.CutOff are public.
func TestReport_CutOffIsNotAnAnswer(t *testing.T) {
	stats := NewStats()
	stats.Reserve(5 * time.Second)
	start := time.Now()
	stats.Start(start, 0)

	answered := start.Add(100 * time.Millisecond)
	stats.Record(Result{Method: "a", ScheduledAt: answered, BegunAt: answered, Deadline: answered.Add(time.Second),
		Outcome: Outcome{Category: CategoryOverload, Code: "Unavailable", CodeFromTarget: true, SentAt: answered, DoneAt: answered.Add(time.Millisecond)}})
	cut := start.Add(1500 * time.Millisecond)
	stats.Record(Result{Method: "a", ScheduledAt: cut, BegunAt: cut, Deadline: cut.Add(time.Second),
		Outcome: Outcome{Category: CategoryCutOff, Code: "Internal", SentAt: cut, DoneAt: cut.Add(time.Millisecond)}})
	stats.EndSending(cut)
	stats.Finish(cut.Add(time.Second))

	m := stats.Report().Methods[0]
	if m.LastAnswerAt == nil || *m.LastAnswerAt != 100*time.Millisecond {
		t.Errorf("last answer at %v, want 100ms: the cut-off call at 1.5s got no status", m.LastAnswerAt)
	}
	if len(m.Seconds) < 2 || m.Seconds[1].CutOff != 1 || m.Seconds[1].Overload+m.Seconds[1].Failure != 0 {
		t.Errorf("seconds %+v: want the cut-off call in second 1 as CutOff, not an error status", m.Seconds)
	}
}
