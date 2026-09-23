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
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yhgrwav/leettest/pkg/metrics"
)

// Ground: contract — a bound is a lower bound: it is printed rounded down, so
// the report never states more than is known. A timeout of 1995ms is ">1.99s",
// not ">2.00s"; 999.6ms is ">999ms", not ">1.00s".
func TestBoundAtATimeoutIsPrintedRoundedDown(t *testing.T) {
	for _, tc := range []struct {
		timeout time.Duration
		want    string
	}{
		{1995 * time.Millisecond, ">1.99s"},
		{999_600 * time.Microsecond, ">999ms"},
		{5 * time.Second, ">5.00s"},
	} {
		l := metrics.NewLatencies()
		for range 90 {
			l.Record(10 * time.Millisecond)
		}
		for range 10 {
			l.RecordCensored(tc.timeout)
		}

		if got := formatQuantile(l.Snapshot().Percentile(0.99)); got != tc.want {
			t.Errorf("timeout %v: p99 %q, want %q", tc.timeout, got, tc.want)
		}
	}
}

// Ground: property — whatever the mix of measured and censored values, a
// printed bound, read back as a number, is at most the r-th smallest recorded
// value: a censored call's true value is at least its threshold, so the r-th
// true value is at least that. 1000 random sets, a fixed seed.
func TestPrintedBoundNeverExceedsTheRankedRecordedValue(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	thresholds := []time.Duration{1995 * time.Millisecond, 999_600 * time.Microsecond, 5 * time.Second, 3200 * time.Millisecond}

	for set := range 1000 {
		l := metrics.NewLatencies()
		var raw []time.Duration
		for range 20 + rng.IntN(200) {
			var d time.Duration
			if rng.IntN(3) == 0 {
				d = thresholds[rng.IntN(1+rng.IntN(len(thresholds)))]
				l.RecordCensored(d)
			} else {
				d = time.Duration(rng.Int64N(int64(6 * time.Second)))
				l.Record(d)
			}
			raw = append(raw, d)
		}
		slices.Sort(raw)

		snap := l.Snapshot()
		for _, p := range []float64{0.5, 0.9, 0.95, 0.99} {
			q := snap.Percentile(p)
			if q.Exact {
				continue
			}
			printed, err := time.ParseDuration(strings.TrimPrefix(formatQuantile(q), ">"))
			if err != nil {
				t.Fatal(err)
			}
			rank := rankOf(p, len(raw))
			if printed > raw[rank-1] {
				t.Fatalf("set %d, p%v: printed %v, but the %d-th recorded value is %v", set, p*100, printed, rank, raw[rank-1])
			}
		}
	}
}

// rankOf is the 1-based rank of the p-th percentile among n values, the
// nearest-rank definition the histograms use.
func rankOf(p float64, n int) int {
	r := int(p*float64(n) + 0.999999999)

	return max(1, min(r, n))
}
