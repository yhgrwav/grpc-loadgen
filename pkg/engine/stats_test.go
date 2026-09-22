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

	"github.com/yhgrwav/leettest/pkg/metrics"
)

// Ground: boundary — which rank is p50 and p99: end-to-end only the stop tests catch p99 read as
// p95, and by chance (mutation 2026-09-22).
func TestStatsReportsPercentiles(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for i := 1; i <= 100; i++ {
		stats.Record(Result{
			Method:      "a",
			ScheduledAt: start,
			Outcome:     Outcome{DoneAt: start.Add(time.Duration(i) * time.Millisecond), Category: CategorySuccess},
		})
	}

	report := stats.Report()
	if len(report.Methods) != 1 {
		t.Fatalf("methods = %d, want 1", len(report.Methods))
	}

	// The histogram answers within its relative accuracy, so the check is a
	// bound rather than equality.
	for _, tt := range []struct {
		name string
		got  metrics.Quantile
		want time.Duration
	}{
		{"p50", report.Methods[0].P50, 50 * time.Millisecond},
		{"p95", report.Methods[0].P95, 95 * time.Millisecond},
		{"p99", report.Methods[0].P99, 99 * time.Millisecond},
		{"max", report.Methods[0].Max, 100 * time.Millisecond},
	} {
		if !tt.got.Defined || !tt.got.Exact {
			t.Errorf("%s = %+v, want an exact defined value", tt.name, tt.got)
			continue
		}
		if tt.got.Value < tt.want || tt.got.Value > tt.want+tt.want/1024+time.Microsecond {
			t.Errorf("%s = %s, want %s", tt.name, tt.got.Value, tt.want)
		}
	}
}

// Ground: boundary — zero observations.
func TestStatsLeavesPercentileUndefinedWithoutObservations(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	// The call never reached the target, so the method exists in the report but
	// has nothing to build a percentile from.
	stats.Record(Result{
		Method:      "a",
		ScheduledAt: start,
		Outcome:     Outcome{DoneAt: start.Add(time.Millisecond), Category: CategoryUnreachable},
	})

	report := stats.Report()

	if got := report.Methods[0].P99; got.Defined {
		t.Errorf("p99 = %+v, want undefined: nothing was measured", got)
	}
	if got := report.Methods[0].Sent; got != 1 {
		t.Errorf("sent = %d, want the failed request counted", got)
	}
}

// Ground: contract — one MethodReport per method.
func TestStatsSplitsMethods(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(Result{Method: "a", ScheduledAt: start, Outcome: Outcome{DoneAt: start.Add(10 * time.Millisecond), Category: CategorySuccess}})
	stats.Record(Result{Method: "a", ScheduledAt: start, Outcome: Outcome{DoneAt: start.Add(20 * time.Millisecond), Category: CategorySuccess}})
	stats.Record(Result{Method: "b", ScheduledAt: start, Outcome: Outcome{DoneAt: start.Add(30 * time.Millisecond), Category: CategoryServerFault, Err: ErrFakeFailure}})

	stats.Finish(start.Add(time.Second))

	report := stats.Report()

	if report.Sent != 3 || report.Failed != 1 {
		t.Fatalf("sent %d failed %d, want 3 and 1", report.Sent, report.Failed)
	}
	if len(report.Methods) != 2 {
		t.Fatalf("methods = %d, want 2", len(report.Methods))
	}
	if report.Methods[0].Method != "a" || report.Methods[0].Sent != 2 {
		t.Errorf("first method = %q with %d requests, want a with 2", report.Methods[0].Method, report.Methods[0].Sent)
	}
	if got := report.Methods[0].Max; !got.Defined || got.Value < 20*time.Millisecond {
		t.Errorf("max = %+v, want at least 20ms", got)
	}
}

// Ground: boundary — a category outside the enum, which no real sender returns.
func TestStatsCountsUnfilledCategoryAsFailure(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(Result{Method: "a", ScheduledAt: start, Outcome: Outcome{DoneAt: start.Add(time.Millisecond)}})

	report := stats.Report()

	if report.Sent != 1 || report.Failed != 1 {
		t.Fatalf("sent %d failed %d, want a sender that forgot Category to count as failed", report.Sent, report.Failed)
	}
}

// Ground: contract — a refused connection is not latency; end-to-end an unreachable target ends the
// run before any report.
func TestStatsKeepsUnreachableCallsOutOfLatency(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for range 9 {
		stats.Record(Result{
			Method:      "a",
			ScheduledAt: start,
			Outcome:     Outcome{DoneAt: start.Add(100 * time.Millisecond), Category: CategorySuccess},
		})
	}
	// A refused connection comes back almost instantly; counted as a latency it
	// would drag the median down while the target is in fact unreachable.
	stats.Record(Result{
		Method:      "a",
		ScheduledAt: start,
		Outcome:     Outcome{DoneAt: start.Add(50 * time.Microsecond), Category: CategoryUnreachable},
	})

	report := stats.Report()
	method := report.Methods[0]

	if method.Unanswered != 1 {
		t.Errorf("unanswered = %d, want 1", method.Unanswered)
	}
	if method.Latencies != 9 {
		t.Errorf("latencies = %d, want 9: an unreachable call is not a measurement", method.Latencies)
	}
	if method.Failed != 1 {
		t.Errorf("failed = %d, want 1: it still failed", method.Failed)
	}
	if got := method.P50; !got.Defined || got.Value < 100*time.Millisecond {
		t.Errorf("p50 = %+v, want the median of the calls that answered", got)
	}
}

// Ground: boundary — the threshold is the deadline to the nanosecond, not a later moment.
func TestStatsCensorsAtTheDeadlineNotAtTheReport(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	// The sender noticed the timeout 40ms after the deadline; only the deadline
	// itself is a justified lower bound.
	stats.Record(Result{
		Method:      "a",
		ScheduledAt: start,
		Deadline:    start.Add(time.Second),
		Outcome:     Outcome{DoneAt: start.Add(1040 * time.Millisecond), Category: CategoryTimeout},
	})

	report := stats.Report()

	if got := report.Methods[0].P99; got.Value >= 1040*time.Millisecond {
		t.Errorf("bound = %s, want the deadline of 1s, not the moment the timeout was noticed", got.Value)
	}
	if got := report.Methods[0].P99; got.Value < time.Second {
		t.Errorf("bound = %s, want at least the deadline", got.Value)
	}
}

// Ground: contract — Options.Warmup; no end-to-end test reaches warmup yet.
func TestStatsExcludesWarmupFromCountsAndRate(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, time.Second)

	stats.Record(Result{
		Method:      "a",
		ScheduledAt: start,
		Outcome:     Outcome{DoneAt: start.Add(time.Millisecond), Category: CategoryServerFault},
	})
	stats.Record(Result{
		Method:      "a",
		ScheduledAt: start.Add(1500 * time.Millisecond),
		Outcome:     Outcome{DoneAt: start.Add(1510 * time.Millisecond), Category: CategorySuccess},
	})

	stats.Finish(start.Add(3 * time.Second))

	report := stats.Report()

	if report.Sent != 1 || report.Failed != 0 {
		t.Fatalf("sent %d failed %d, want the warmup request left out of both", report.Sent, report.Failed)
	}
	// Two measured seconds of the three, one request: the rate divides by the
	// measured window, not by the whole run.
	if got := report.Methods[0].RPS; got < 0.49 || got > 0.51 {
		t.Errorf("rps = %.3f, want ~0.5: one request over the two measured seconds", got)
	}
}
