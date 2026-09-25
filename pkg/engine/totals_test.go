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

// finished builds one finished call scheduled at the given offset from start.
type finished struct {
	method   string
	at       time.Duration
	category Category
	// notSent with blocker: a timeout that never went out. lag is how late it
	// began; the deadline is one second after the schedule.
	notSent bool
	blocker Blocker
	lag     time.Duration
}

func (c finished) result(start time.Time) Result {
	scheduled := start.Add(c.at)
	begun := scheduled.Add(c.lag)
	deadline := scheduled.Add(time.Second)

	r := Result{Method: c.method, ScheduledAt: scheduled, BegunAt: begun, Deadline: deadline}
	r.Category = c.category
	switch {
	case c.notSent:
		r.Category = CategoryTimeout
		r.NotSent = true
		r.NotSentOn = c.blocker
		r.DoneAt = deadline
	case c.category == CategoryTimeout:
		r.SentAt = begun
		r.DoneAt = deadline
	default:
		r.SentAt = begun
		r.DoneAt = begun.Add(10 * time.Millisecond)
	}

	return r
}

// mixed covers every way a call can end, on two methods, in and after a
// one-second warmup. The unsent calls include one the sender blamed on the
// generator while its lag was short: the one the two rules used to split
// differently.
func mixed() []finished {
	var calls []finished
	for _, m := range []string{"a", "b"} {
		for _, at := range []time.Duration{200 * time.Millisecond, 1500 * time.Millisecond, 2500 * time.Millisecond} {
			calls = append(calls,
				finished{method: m, at: at, category: CategorySuccess},
				finished{method: m, at: at, category: CategoryServerFault},
				finished{method: m, at: at, category: CategoryOverload},
				finished{method: m, at: at, category: CategoryClientFault},
				finished{method: m, at: at, category: CategoryTimeout},
				finished{method: m, at: at, category: CategoryUnreachable},
				finished{method: m, at: at, category: CategoryCutOff},
				finished{method: m, at: at, category: CategoryAborted},
				finished{method: m, at: at, category: CategoryUnknown},
				finished{method: m, at: at, notSent: true, blocker: BlockedOnGenerator, lag: 900 * time.Millisecond},
				finished{method: m, at: at, notSent: true, blocker: BlockedOnGenerator, lag: 10 * time.Millisecond},
				finished{method: m, at: at, notSent: true, blocker: BlockedOnStream},
				finished{method: m, at: at, notSent: true, blocker: BlockedOnConnection},
			)
		}
	}

	return calls
}

func reportOf(calls []finished, warmup time.Duration) Report {
	stats := NewStats()
	stats.Reserve(6*time.Second, "a", "b")
	start := time.Now()
	stats.Start(start, warmup)
	for _, c := range calls {
		stats.Record(c.result(start))
	}
	stats.EndSending(start.Add(3 * time.Second))
	stats.Finish(start.Add(5 * time.Second))

	return stats.Report()
}

// Every call of a method is on its seconds exactly once, warmup included, and
// the seconds split the unsent calls by the rule of the totals.
func TestTotals_SecondsAddUpToTheMethodsTotals(t *testing.T) {
	report := reportOf(mixed(), time.Second)

	for _, m := range report.Methods {
		var begun, ended, late, stream, conn int
		for _, s := range m.Seconds {
			begun += s.Begun
			ended += s.Succeeded + s.TargetFailed + s.TimedOut + s.RequestFailed +
				s.NotSentLate + s.NotSentStream + s.NotSentConnection +
				s.Unanswered + s.CutOff + s.Aborted + s.Unclassified
			late += s.NotSentLate
			stream += s.NotSentStream
			conn += s.NotSentConnection
		}

		all := m.Sent + m.NotSent + m.WarmupSent + m.WarmupNotSent
		if m.OutsideTimeline != 0 {
			t.Fatalf("%s: %d calls off the timeline, want none in this run", m.Method, m.OutsideTimeline)
		}
		if begun != all || ended != all {
			t.Errorf("%s: seconds begun %d, ended %d, want every call once: sent %d + not sent %d + warmup sent %d + warmup not sent %d = %d",
				m.Method, begun, ended, m.Sent, m.NotSent, m.WarmupSent, m.WarmupNotSent, all)
		}
		if last := m.Seconds[len(m.Seconds)-1].InFlight; last != 0 {
			t.Errorf("%s: %d still in flight at the end of a finished run", m.Method, last)
		}

		// Without warmup's share: 2 of the 3 schedule points are measured.
		if want := m.NotSentLate + 2; late != want {
			t.Errorf("%s: seconds not sent late %d, want the method's %d plus warmup's", m.Method, late, want)
		}
		if want := m.NotSentStream + 1; stream != want {
			t.Errorf("%s: seconds not sent on stream %d, want %d", m.Method, stream, want)
		}
		if want := m.NotSentConnection + 1; conn != want {
			t.Errorf("%s: seconds not sent on connection %d, want %d", m.Method, conn, want)
		}
	}
}

// Each method gets its share of the totals, and the totals are their sum.
func TestTotals_TheRunIsTheSumOfItsMethods(t *testing.T) {
	report := reportOf(mixed(), time.Second)

	var sum MethodReport
	for _, m := range report.Methods {
		// Two measured schedule points, 13 calls each: 9 went out, 4 did not.
		if m.Sent != 18 || m.NotSent != 8 || m.Aborted != 2 || m.Failed != 14 {
			t.Errorf("%s: sent %d, not sent %d, aborted %d, failed %d; want 18, 8, 2, 14",
				m.Method, m.Sent, m.NotSent, m.Aborted, m.Failed)
		}
		if m.NotSentLate != 4 || m.NotSentStream != 2 || m.NotSentConnection != 2 {
			t.Errorf("%s: not sent late %d, stream %d, connection %d; want 4, 2, 2",
				m.Method, m.NotSentLate, m.NotSentStream, m.NotSentConnection)
		}
		if m.WarmupSent != 9 || m.WarmupFailed != 7 || m.WarmupNotSent != 4 {
			t.Errorf("%s: warmup sent %d, failed %d, not sent %d; want 9, 7, 4",
				m.Method, m.WarmupSent, m.WarmupFailed, m.WarmupNotSent)
		}

		sum.Sent += m.Sent
		sum.Failed += m.Failed
		sum.Aborted += m.Aborted
		sum.NotSent += m.NotSent
		sum.NotSentLate += m.NotSentLate
		sum.NotSentStream += m.NotSentStream
		sum.NotSentConnection += m.NotSentConnection
		sum.WarmupSent += m.WarmupSent
		sum.WarmupFailed += m.WarmupFailed
		sum.WarmupNotSent += m.WarmupNotSent
	}

	got := MethodReport{
		Sent: report.Sent, Failed: report.Failed, Aborted: report.Aborted, NotSent: report.NotSent,
		NotSentLate: report.NotSentLate, NotSentStream: report.NotSentStream, NotSentConnection: report.NotSentConnection,
		WarmupSent: report.WarmupSent, WarmupFailed: report.WarmupFailed, WarmupNotSent: report.WarmupNotSent,
	}
	if got.Sent != sum.Sent || got.Failed != sum.Failed || got.Aborted != sum.Aborted || got.NotSent != sum.NotSent ||
		got.NotSentLate != sum.NotSentLate || got.NotSentStream != sum.NotSentStream || got.NotSentConnection != sum.NotSentConnection ||
		got.WarmupSent != sum.WarmupSent || got.WarmupFailed != sum.WarmupFailed || got.WarmupNotSent != sum.WarmupNotSent {
		t.Errorf("run totals %+v, want the sum of the methods %+v", got, sum)
	}
}

// A warmup call that timed out before going out used to be counted nowhere.
func TestTotals_AnUnsentWarmupCallIsCounted(t *testing.T) {
	report := reportOf([]finished{{method: "a", at: 100 * time.Millisecond, notSent: true, blocker: BlockedOnStream}}, time.Second)

	if report.WarmupNotSent != 1 {
		t.Errorf("run warmup not sent = %d, want 1", report.WarmupNotSent)
	}
	if m := report.Methods[0]; m.WarmupNotSent != 1 || m.WarmupSent != 0 || m.NotSent != 0 {
		t.Errorf("method warmup not sent %d, warmup sent %d, not sent %d; want 1, 0, 0",
			m.WarmupNotSent, m.WarmupSent, m.NotSent)
	}
	if report.NotSent != 0 || report.WarmupSent != 0 {
		t.Errorf("run not sent %d, warmup sent %d; want 0, 0: warmup is out of the measured totals",
			report.NotSent, report.WarmupSent)
	}
}
