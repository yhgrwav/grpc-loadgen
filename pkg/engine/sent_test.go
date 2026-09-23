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
