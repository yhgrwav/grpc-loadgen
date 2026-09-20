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
	"sort"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// Snapshot is a consistent copy of a distribution, sharing no state with the
// Latencies that keeps recording into it.
type Snapshot struct {
	measuredCum []cumBar
	combinedCum []cumBar
	measuredN   int64
	censoredN   int64
	censoredMin int64
}

// Quantile is one percentile of the distribution.
type Quantile struct {
	Value time.Duration
	// Exact is false when the percentile landed among censored observations:
	// the true value is larger than Value by an unknown amount.
	Exact bool
}

// cumBar is one non-empty histogram bucket with the running count of all
// observations at or below it, used to answer rank queries by binary search
// instead of a fresh Distribution() walk per percentile.
type cumBar struct {
	upperMicros int64
	cumCount    int64
}

// Snapshot copies the counters under the lock and does every computation
// outside it: holding the lock through the arithmetic would stall recording
// on the hot path.
func (l *Latencies) Snapshot() *Snapshot {
	l.mu.Lock()
	measuredRaw := l.measured.Export()
	censoredRaw := l.censored.Export()
	l.mu.Unlock()

	measured := hdrhistogram.Import(measuredRaw)
	censored := hdrhistogram.Import(censoredRaw)
	combined := hdrhistogram.Import(sumCounts(measuredRaw, censoredRaw))

	censoredN := censored.TotalCount()
	censoredMin := int64(math.MaxInt64)
	if censoredN > 0 {
		censoredMin = censored.Min()
	}

	return &Snapshot{
		measuredCum: cumulative(measured),
		combinedCum: cumulative(combined),
		measuredN:   measured.TotalCount(),
		censoredN:   censoredN,
		censoredMin: censoredMin,
	}
}

// Count reports every observation, measured and censored alike.
func (s *Snapshot) Count() int64 {
	return s.measuredN + s.censoredN
}

func (s *Snapshot) CensoredCount() int64 {
	return s.censoredN
}

// Percentile reports the p-th percentile, p given as a fraction in [0, 1] —
// not as 0..100. Values outside the range are clamped.
//
// The result is exact only when no censored observation could have taken that
// rank; otherwise Value is a lower bound. See Quantile.Exact.
func (s *Snapshot) Percentile(p float64) Quantile {
	n := s.Count()
	if n == 0 {
		return Quantile{Exact: true}
	}

	rank := rankFor(clampFraction(p), n)
	if countAtOrBelow(s.measuredCum, s.censoredMin) >= rank {
		return Quantile{Value: microsToDuration(valueAtRank(s.measuredCum, rank)), Exact: true}
	}
	return Quantile{Value: microsToDuration(valueAtRank(s.combinedCum, rank)), Exact: false}
}

func rankFor(p float64, n int64) int64 {
	rank := int64(math.Ceil(p * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return rank
}

func clampFraction(p float64) float64 {
	switch {
	case p < 0:
		return 0
	case p > 1:
		return 1
	default:
		return p
	}
}

func microsToDuration(micros int64) time.Duration {
	return time.Duration(micros) * time.Microsecond
}

// sumCounts merges two same-shaped histograms by adding their raw count
// arrays. Cheaper than Histogram.Merge, which walks and re-records every bar.
func sumCounts(a, b *hdrhistogram.Snapshot) *hdrhistogram.Snapshot {
	counts := make([]int64, len(a.Counts))
	for i, v := range a.Counts {
		counts[i] = v + b.Counts[i]
	}
	return &hdrhistogram.Snapshot{
		LowestTrackableValue:  a.LowestTrackableValue,
		HighestTrackableValue: a.HighestTrackableValue,
		SignificantFigures:    a.SignificantFigures,
		Counts:                counts,
	}
}

func cumulative(h *hdrhistogram.Histogram) []cumBar {
	bars := h.Distribution()
	cum := make([]cumBar, 0, len(bars))
	var running int64
	for _, b := range bars {
		if b.Count == 0 {
			continue
		}
		running += b.Count
		cum = append(cum, cumBar{upperMicros: b.To, cumCount: running})
	}
	return cum
}

// countAtOrBelow returns the count of observations in buckets whose upper
// bound does not exceed threshold: every one of them is guaranteed below any
// censored observation with a threshold of at least threshold, since a
// censored observation's true value always exceeds its own threshold.
func countAtOrBelow(cum []cumBar, threshold int64) int64 {
	idx := sort.Search(len(cum), func(i int) bool { return cum[i].upperMicros > threshold })
	if idx == 0 {
		return 0
	}
	return cum[idx-1].cumCount
}

func valueAtRank(cum []cumBar, rank int64) int64 {
	idx := sort.Search(len(cum), func(i int) bool { return cum[i].cumCount >= rank })
	if idx == len(cum) {
		if len(cum) == 0 {
			return 0
		}
		idx = len(cum) - 1
	}
	return cum[idx].upperMicros
}
