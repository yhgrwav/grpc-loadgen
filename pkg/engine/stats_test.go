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

func TestPercentileSorted(t *testing.T) {
	values := make([]time.Duration, 0, 100)
	for i := 1; i <= 100; i++ {
		values = append(values, time.Duration(i)*time.Millisecond)
	}

	tests := []struct {
		p    int
		want time.Duration
	}{
		{p: 50, want: 50 * time.Millisecond},
		{p: 95, want: 95 * time.Millisecond},
		{p: 99, want: 99 * time.Millisecond},
		{p: 100, want: 100 * time.Millisecond},
	}

	for _, tt := range tests {
		if got := percentileSorted(values, tt.p); got != tt.want {
			t.Errorf("p%d = %s, want %s", tt.p, got, tt.want)
		}
	}
}

func TestPercentileOfEmptySetIsZero(t *testing.T) {
	if got := percentileSorted(nil, 99); got != 0 {
		t.Errorf("p99 of nothing = %s, want 0", got)
	}
}

func TestStatsSplitsMethods(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(Result{Method: "a", ScheduledAt: start, Outcome: Outcome{DoneAt: start.Add(10 * time.Millisecond)}})
	stats.Record(Result{Method: "a", ScheduledAt: start, Outcome: Outcome{DoneAt: start.Add(20 * time.Millisecond)}})
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
	if report.Methods[0].Max != 20*time.Millisecond {
		t.Errorf("max = %s, want 20ms", report.Methods[0].Max)
	}
}
