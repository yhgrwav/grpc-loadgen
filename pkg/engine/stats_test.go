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

	"github.com/yhgrwav/grpc-loadgen/pkg/metrics"
)

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

func TestStatsReportsTimeoutAsLowerBound(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	// p99 of 100 observations is the 99th; with three of them abandoned it can
	// no longer be pinned down, while p50 still can.
	for range 97 {
		stats.Record(Result{
			Method:      "a",
			ScheduledAt: start,
			Outcome:     Outcome{DoneAt: start.Add(10 * time.Millisecond), Category: CategorySuccess},
		})
	}
	for range 3 {
		stats.Record(Result{
			Method:      "a",
			ScheduledAt: start,
			Outcome:     Outcome{DoneAt: start.Add(time.Second), Category: CategoryTimeout},
		})
	}

	report := stats.Report()

	if got := report.Methods[0].P99; got.Exact {
		t.Errorf("p99 = %+v, want a lower bound: the tail ran past the deadline", got)
	}
	if got := report.Methods[0].P99; !got.Defined || got.Value < time.Second {
		t.Errorf("p99 = %+v, want a bound of at least the deadline", got)
	}
	if got := report.Methods[0].Censored; got != 3 {
		t.Errorf("censored = %d, want 3", got)
	}
	if got := report.Methods[0].P50; !got.Exact {
		t.Errorf("p50 = %+v, want an exact value: the timeout sits above it", got)
	}
}

func TestStatsLeavesPercentileUndefinedWithoutObservations(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, time.Minute)

	// Inside the warmup window, so it is counted but not measured.
	stats.Record(Result{
		Method:      "a",
		ScheduledAt: start,
		Outcome:     Outcome{DoneAt: start.Add(time.Millisecond), Category: CategorySuccess},
	})

	report := stats.Report()

	if got := report.Methods[0].P99; got.Defined {
		t.Errorf("p99 = %+v, want undefined: nothing was measured", got)
	}
	if got := report.Methods[0].Sent; got != 1 {
		t.Errorf("sent = %d, want the warmup request still counted", got)
	}
}

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
