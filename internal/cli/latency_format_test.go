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

package cli

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/yhgrwav/leettest/pkg/metrics"
)

// Ground: contract — the histograms keep 3 significant figures, so a latency
// is printed with 3, rounded half away from zero: fewer hides a threefold
// difference below a millisecond, more states digits the data does not have.
// A value that rounds up to the next unit moves to it.
func TestFormatLatencyPrintsThreeSignificantFigures(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{300 * time.Microsecond, "300us"},
		{999 * time.Microsecond, "999us"},
		{999_600 * time.Nanosecond, "1.00ms"},
		{time.Millisecond, "1.00ms"},
		{10_340 * time.Microsecond, "10.3ms"},
		{10_350 * time.Microsecond, "10.4ms"},
		{999_500 * time.Microsecond, "1.00s"},
		{1_705 * time.Millisecond, "1.71s"},
		{1_795 * time.Millisecond, "1.80s"},
		{59_940 * time.Millisecond, "59.9s"},
		{59_990 * time.Millisecond, "1m00s"},
		{59_996 * time.Millisecond, "1m00s"},
		{95_500 * time.Millisecond, "1m36s"},
		{312 * time.Microsecond, "312us"},
		{12_340 * time.Millisecond, "12.3s"},
		{511 * time.Millisecond, "511ms"},
		{999 * time.Nanosecond, "999ns"},
	} {
		if got := formatLatency(tc.in); got != tc.want {
			t.Errorf("formatLatency(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Ground: boundary — the widest latency the histograms can hold, as a bound,
// fits the 8 columns the tables were laid out for.
func TestFormatLatencyIsNoWiderThanEightColumns(t *testing.T) {
	var widest string
	for d := time.Duration(1); d <= time.Hour; d = d*101/100 + 1 {
		s := formatQuantile(metrics.Quantile{Value: d, Defined: true})
		if lipgloss.Width(s) > lipgloss.Width(widest) {
			widest = s
		}
	}
	if lipgloss.Width(widest) > 8 {
		t.Errorf("%q is %d columns wide, more than 8", widest, lipgloss.Width(widest))
	}
}

// Ground: contract — a duration keeps its precision but obeys the rule the
// latencies do: rounded, not cut, and a value that rounds up to the next unit
// moves to it. Cut, a run of 95.9s reads 1m35s.
func TestFormatDurationRoundsAndMovesToTheNextUnit(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{95_900 * time.Millisecond, "1m36s"},
		{999_600 * time.Nanosecond, "1ms"},
		{59_960 * time.Millisecond, "1m00s"},
		{12 * time.Second, "12.0s"},
		{100 * time.Millisecond, "100ms"},
	} {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func BenchmarkFormatQuantile(b *testing.B) {
	q := metrics.Quantile{Value: 10_340 * time.Microsecond, Exact: true, Defined: true}
	b.ReportAllocs()
	for b.Loop() {
		_ = formatQuantile(q)
	}
}
