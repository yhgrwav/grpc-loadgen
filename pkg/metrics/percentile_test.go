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

package metrics

import (
	"math"
	"slices"
	"testing"
)

// fakeView is a distribution whose buckets are its values: each observation is
// its own bucket's upper bound. Values under 2048ns keep lowestEquivalent and
// highestEquivalent the identity, so the rule is checked apart from the HDR shape.
type fakeView struct {
	measured, censored []int64
	combinedCalls      int
}

func (f *fakeView) counts() (measured, censored int64) {
	return int64(len(f.measured)), int64(len(f.censored))
}

func (f *fakeView) thresholds() (lo, hi int64) {
	if len(f.censored) == 0 {
		return math.MaxInt64, 0
	}
	return slices.Min(f.censored), slices.Max(f.censored)
}

func (f *fakeView) measuredAt(rank int64) int64 {
	s := slices.Sorted(slices.Values(f.measured))
	return s[rank-1]
}

func (f *fakeView) combinedAt(rank int64) int64 {
	f.combinedCalls++
	s := slices.Sorted(slices.Values(append(slices.Clone(f.measured), f.censored...)))
	return s[rank-1]
}

// Ground: boundary — the decision among exact value, single threshold and bucket bottom, each
// branch and each edge between them, on data where the right answer is read off by eye.
func TestPercentileRule(t *testing.T) {
	cases := []struct {
		name               string
		measured, censored []int64
		p                  float64
		want               Quantile
	}{
		{"empty", nil, nil, 0.5, Quantile{}},
		{"no censored: exact rank value", []int64{10, 20, 30, 40}, nil, 0.5, Quantile{20, true, true}},
		{"measured at rank equals the smallest threshold: exact", []int64{10, 50}, []int64{50}, 0.5, Quantile{50, true, true}},
		{"measured at rank above the only threshold: the threshold", []int64{10, 60, 70}, []int64{50}, 0.5, Quantile{50, false, true}},
		{"rank past every measured value: the threshold", []int64{10}, []int64{50, 50, 50}, 0.75, Quantile{50, false, true}},
		{"mixed thresholds: the rank in the combined order", []int64{10, 100}, []int64{30, 50}, 0.75, Quantile{50, false, true}},
		{"only censored, mixed: the rank in the combined order", nil, []int64{30, 50, 70}, 1, Quantile{70, false, true}},
		{"p = 0 is rank 1", []int64{10, 20}, nil, 0, Quantile{10, true, true}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := &fakeView{measured: c.measured, censored: c.censored}
			if got := percentile(v, c.p); got != c.want {
				t.Errorf("percentile(%v) = %+v, want %+v", c.p, got, c.want)
			}
		})
	}
}

// Ground: contract — the combined order costs a merge in Buffer, so an exact percentile must not
// ask for it.
func TestPercentileRule_ExactDoesNotBuildTheCombinedOrder(t *testing.T) {
	v := &fakeView{measured: []int64{10, 20, 30}, censored: []int64{500}}

	for _, p := range fractions {
		percentile(v, p)
	}

	if v.combinedCalls != 0 {
		t.Errorf("combinedAt called %d times, want 0", v.combinedCalls)
	}
}

// Ground: boundary — p outside [0, 1] is a caller mistake, panicking rather than clamping.
func TestPercentileRule_FractionOutsideRangePanics(t *testing.T) {
	for _, p := range []float64{-0.01, 1.01, 99} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("percentile(%v) did not panic", p)
				}
			}()
			percentile(&fakeView{measured: []int64{1}}, p)
		}()
	}
}
