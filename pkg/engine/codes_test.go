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

	want := []CodeCount{{"Unavailable", 5}, {"Aborted", 2}, {"Internal", 2}}
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

	want := []CodeCount{{"DeadlineExceeded", 1}, {"Unavailable", 1}}
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
