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

// oneCall records a single successful call and returns its method's report.
func oneCall(t *testing.T, lag, conn, stream, latency time.Duration) MethodReport {
	t.Helper()

	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	sched := start.Add(10 * time.Millisecond)
	begun := sched.Add(lag)
	stats.Record(Result{Method: "a", ScheduledAt: sched, BegunAt: begun, Deadline: sched.Add(time.Second),
		Outcome: Outcome{Category: CategorySuccess, ConnWait: conn, StreamWait: stream,
			SentAt: begun.Add(conn + stream), DoneAt: sched.Add(latency)}})
	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	return stats.Report().Methods[0]
}

// The p99 the target had is the latency with every client-side wait taken
// out: start lag, the wait for a connection and for a stream. The printed p99
// keeps them all: latency still runs from the schedule.
//
// Ground: contract — MethodReport.P99WithoutClientWaits is public.
func TestReport_P99WithoutClientWaitsTakesOutAllThree(t *testing.T) {
	m := oneCall(t, 20*time.Millisecond, 50*time.Millisecond, 5*time.Millisecond, 80*time.Millisecond)

	if got := m.P99WithoutClientWaits.Value; got < 4900*time.Microsecond || got > 5100*time.Microsecond {
		t.Errorf("p99 without client waits = %v, want 5ms (80 - 20 - 50 - 5)", got)
	}
	if got := m.P99.Value; got < 79*time.Millisecond || got > 81*time.Millisecond {
		t.Errorf("printed p99 = %v, want 80ms: it still runs from the schedule", got)
	}
}

// The three waits do not overlap, so they never add up past the latency. If
// they do, the arithmetic is wrong somewhere: the difference is clamped to
// zero rather than recorded as a negative time, and the stand invariant in
// grpcsender is what catches it.
//
// Ground: boundary — a negative service time would pull the distribution down.
func TestReport_ClientWaitsPastTheLatencyClampToZero(t *testing.T) {
	m := oneCall(t, 10*time.Millisecond, 10*time.Millisecond, 0, 15*time.Millisecond)

	if !m.P99WithoutClientWaits.Defined || m.P99WithoutClientWaits.Value != 0 {
		t.Errorf("p99 without client waits = %+v, want a defined 0", m.P99WithoutClientWaits)
	}
	if m.Invalid != 0 {
		t.Errorf("invalid = %d: the printed latency is fine, only the difference is not", m.Invalid)
	}
}
