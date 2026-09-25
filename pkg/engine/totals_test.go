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
		calls = append(calls,
			// Scheduled in the warmup's last millisecond, begun after it:
			// warmup by its schedule, on second 1 by its start.
			finished{method: m, at: 999 * time.Millisecond, category: CategorySuccess, lag: 5 * time.Millisecond},
			// Past the reserved six seconds: off the timeline, still sent.
			finished{method: m, at: 7 * time.Second, category: CategorySuccess},
		)
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
				s.NotSentGenerator + s.NotSentStream + s.NotSentConnection +
				s.Unanswered + s.CutOff + s.Aborted + s.Unclassified
			late += s.NotSentGenerator
			stream += s.NotSentStream
			conn += s.NotSentConnection
		}

		all := m.Sent + m.NotSent + m.WarmupSent + m.WarmupNotSent
		if m.OutsideTimeline != 1 {
			t.Errorf("%s: %d calls off the timeline, want the one past its end", m.Method, m.OutsideTimeline)
		}
		if begun+m.OutsideTimeline != all || ended+m.OutsideTimeline != all {
			t.Errorf("%s: seconds begun %d, ended %d, + off the timeline %d, want every call once: sent %d + not sent %d + warmup sent %d + warmup not sent %d = %d",
				m.Method, begun, ended, m.OutsideTimeline, m.Sent, m.NotSent, m.WarmupSent, m.WarmupNotSent, all)
		}
		if last := m.Seconds[len(m.Seconds)-1].InFlight; last != 0 {
			t.Errorf("%s: %d still in flight at the end of a finished run", m.Method, last)
		}

		// Without warmup's share: 2 of the 3 schedule points are measured.
		if want := m.NotSentGenerator + 2; late != want {
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
		// Two measured schedule points, 13 calls each: 9 went out, 4 did not;
		// plus the one off the timeline. Warmup: 9 + the one at its edge.
		if m.Sent != 19 || m.NotSent != 8 || m.Aborted != 2 || m.Failed != 14 {
			t.Errorf("%s: sent %d, not sent %d, aborted %d, failed %d; want 19, 8, 2, 14",
				m.Method, m.Sent, m.NotSent, m.Aborted, m.Failed)
		}
		if m.NotSentGenerator != 4 || m.NotSentStream != 2 || m.NotSentConnection != 2 {
			t.Errorf("%s: not sent late %d, stream %d, connection %d; want 4, 2, 2",
				m.Method, m.NotSentGenerator, m.NotSentStream, m.NotSentConnection)
		}
		if m.WarmupSent != 10 || m.WarmupFailed != 7 || m.WarmupNotSent != 4 {
			t.Errorf("%s: warmup sent %d, failed %d, not sent %d; want 10, 7, 4",
				m.Method, m.WarmupSent, m.WarmupFailed, m.WarmupNotSent)
		}

		sum.Sent += m.Sent
		sum.Failed += m.Failed
		sum.Aborted += m.Aborted
		sum.NotSent += m.NotSent
		sum.NotSentGenerator += m.NotSentGenerator
		sum.NotSentStream += m.NotSentStream
		sum.NotSentConnection += m.NotSentConnection
		sum.WarmupSent += m.WarmupSent
		sum.WarmupFailed += m.WarmupFailed
		sum.WarmupNotSent += m.WarmupNotSent
	}

	got := MethodReport{
		Sent: report.Sent, Failed: report.Failed, Aborted: report.Aborted, NotSent: report.NotSent,
		NotSentGenerator: report.NotSentGenerator, NotSentStream: report.NotSentStream, NotSentConnection: report.NotSentConnection,
		WarmupSent: report.WarmupSent, WarmupFailed: report.WarmupFailed, WarmupNotSent: report.WarmupNotSent,
	}
	if got.Sent != sum.Sent || got.Failed != sum.Failed || got.Aborted != sum.Aborted || got.NotSent != sum.NotSent ||
		got.NotSentGenerator != sum.NotSentGenerator || got.NotSentStream != sum.NotSentStream || got.NotSentConnection != sum.NotSentConnection ||
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

// A sender that forgot NotSentOn: the call is in NotSent but in none of its
// causes, so the causes fall short of it — a visible defect, not a quiet
// connection problem. The late start would name the generator by timing; it
// must not cover the missing cause.
func TestTotals_AnUnsentCallWithoutACauseIsInNoCause(t *testing.T) {
	r := finished{method: "a", at: 1500 * time.Millisecond, notSent: true, lag: 900 * time.Millisecond}
	report := reportOf([]finished{r}, time.Second)

	m := report.Methods[0]
	if m.NotSent != 1 || m.NotSentGenerator+m.NotSentStream+m.NotSentConnection != 0 {
		t.Errorf("not sent %d, by cause %d/%d/%d; want 1 and none by cause",
			m.NotSent, m.NotSentGenerator, m.NotSentStream, m.NotSentConnection)
	}
	var s Second
	for _, sec := range m.Seconds {
		s.NotSentGenerator += sec.NotSentGenerator
		s.NotSentStream += sec.NotSentStream
		s.NotSentConnection += sec.NotSentConnection
		s.Unclassified += sec.Unclassified
	}
	s.InFlight = m.Seconds[len(m.Seconds)-1].InFlight
	if s.NotSentGenerator+s.NotSentStream+s.NotSentConnection != 0 || s.Unclassified != 1 || s.InFlight != 0 {
		t.Errorf("seconds: by cause %d/%d/%d, unclassified %d, in flight %d; want none, 1, 0 at the end",
			s.NotSentGenerator, s.NotSentStream, s.NotSentConnection, s.Unclassified, s.InFlight)
	}
}

// Every unsent call of a well-behaved sender names its cause: the causes add
// up to NotSent on the run, on each method and over each method's seconds.
func TestTotals_TheCausesAddUpToNotSent(t *testing.T) {
	report := reportOf(mixed(), time.Second)

	if got := report.NotSentGenerator + report.NotSentStream + report.NotSentConnection; got != report.NotSent {
		t.Errorf("run: causes add up to %d, not sent %d", got, report.NotSent)
	}
	for _, m := range report.Methods {
		if got := m.NotSentGenerator + m.NotSentStream + m.NotSentConnection; got != m.NotSent {
			t.Errorf("%s: causes add up to %d, not sent %d", m.Method, got, m.NotSent)
		}
		var bySeconds int
		for _, s := range m.Seconds {
			bySeconds += s.NotSentGenerator + s.NotSentStream + s.NotSentConnection
		}
		if want := m.NotSent + m.WarmupNotSent; bySeconds != want {
			t.Errorf("%s: seconds' causes add up to %d, want not sent + warmup not sent = %d", m.Method, bySeconds, want)
		}
	}
}
