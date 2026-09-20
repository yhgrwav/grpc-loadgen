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
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// Snapshot is a consistent copy of a distribution, sharing no state with the
// Latencies that keeps recording into it. Cumulative bars are built lazily,
// on the first Percentile call, so a Snapshot that only feeds Merge stays cheap.
type Snapshot struct {
	measuredRaw *hdrhistogram.Snapshot
	censoredRaw *hdrhistogram.Snapshot

	measuredN   int64
	censoredN   int64
	censoredMin int64
	invalidN    int64

	measuredOnce sync.Once
	measuredCum  []cumBar

	combinedOnce sync.Once
	combinedCum  []cumBar
}

// Quantile is one percentile of the distribution.
type Quantile struct {
	Value time.Duration
	// Exact is false when the percentile landed among censored observations:
	// the true value is larger than Value by an unknown amount. Meaningful
	// only when Defined is true.
	Exact bool
	// Defined is false when the distribution has no observations at all;
	// check it before Value or Exact.
	Defined bool
}

// cumBar is one non-empty histogram bucket with the running count of all
// observations at or below it, used to answer rank queries by binary search
// instead of a fresh Distribution() walk per percentile.
type cumBar struct {
	upperNanos int64
	cumCount   int64
}

// Snapshot copies the counters under the lock and does every computation
// outside it: holding the lock through the arithmetic would stall recording
// on the hot path.
func (l *Latencies) Snapshot() *Snapshot {
	l.mu.Lock()
	measuredRaw := l.measured.Export()
	censoredRaw := l.censored.Export()
	l.mu.Unlock()

	measuredN := hdrhistogram.Import(measuredRaw).TotalCount()
	censored := hdrhistogram.Import(censoredRaw)
	censoredN := censored.TotalCount()
	censoredMin := int64(math.MaxInt64)
	if censoredN > 0 {
		censoredMin = censored.Min()
	}

	return &Snapshot{
		measuredRaw: measuredRaw,
		censoredRaw: censoredRaw,
		measuredN:   measuredN,
		censoredN:   censoredN,
		censoredMin: censoredMin,
		invalidN:    l.invalid.Load(),
	}
}

// Count reports every observation, measured and censored alike.
func (s *Snapshot) Count() int64 {
	return s.measuredN + s.censoredN
}

func (s *Snapshot) CensoredCount() int64 {
	return s.censoredN
}

// InvalidCount reports observations rejected for a negative duration: a
// caller bug, kept separate rather than folded into the distribution.
func (s *Snapshot) InvalidCount() int64 {
	return s.invalidN
}

// Percentile reports the p-th percentile, p given as a fraction in [0, 1] —
// not as 0..100. p outside that range panics: it is a programmer mistake, and
// silently clamping it would print the wrong percentile under the right label.
//
// Check Quantile.Defined first: it is false when the distribution has no
// observations. The result is exact only when no censored observation could
// have taken that rank; otherwise Value is a lower bound. See Quantile.Exact.
func (s *Snapshot) Percentile(p float64) Quantile {
	if p < 0 || p > 1 {
		panic(fmt.Sprintf("metrics: Percentile(%v): p is a fraction in [0, 1], not a 0..100 scale", p))
	}

	n := s.Count()
	if n == 0 {
		return Quantile{}
	}

	rank := rankFor(p, n)
	if countAtOrBelow(s.measuredCumBars(), s.censoredMin) >= rank {
		return Quantile{Value: time.Duration(valueAtRank(s.measuredCumBars(), rank)), Exact: true, Defined: true}
	}
	return Quantile{Value: time.Duration(valueAtRank(s.combinedCumBars(), rank)), Defined: true}
}

// Merge combines snapshots by adding their raw counters, not by calling the
// library's Merge: that walks and re-records every bar and is an order of
// magnitude more expensive.
func Merge(snapshots ...*Snapshot) *Snapshot {
	merged := &Snapshot{censoredMin: math.MaxInt64}
	if len(snapshots) == 0 {
		return merged
	}

	measuredCounts := make([]int64, len(snapshots[0].measuredRaw.Counts))
	censoredCounts := make([]int64, len(snapshots[0].censoredRaw.Counts))
	for _, s := range snapshots {
		addCounts(measuredCounts, s.measuredRaw.Counts)
		addCounts(censoredCounts, s.censoredRaw.Counts)
		merged.measuredN += s.measuredN
		merged.censoredN += s.censoredN
		merged.invalidN += s.invalidN
		if s.censoredN > 0 && s.censoredMin < merged.censoredMin {
			merged.censoredMin = s.censoredMin
		}
	}
	merged.measuredRaw = withCounts(snapshots[0].measuredRaw, measuredCounts)
	merged.censoredRaw = withCounts(snapshots[0].censoredRaw, censoredCounts)
	return merged
}

func (s *Snapshot) measuredCumBars() []cumBar {
	s.measuredOnce.Do(func() {
		s.measuredCum = cumulative(hdrhistogram.Import(s.measuredRaw))
	})
	return s.measuredCum
}

func (s *Snapshot) combinedCumBars() []cumBar {
	s.combinedOnce.Do(func() {
		s.combinedCum = cumulative(hdrhistogram.Import(withCounts(s.measuredRaw, sumOf(s.measuredRaw.Counts, s.censoredRaw.Counts))))
	})
	return s.combinedCum
}

func rankFor(p float64, n int64) int64 {
	product := p * float64(n)
	if nearest := math.Round(product); math.Abs(product-nearest) <= 1e-9*math.Max(1, math.Abs(nearest)) {
		product = nearest
	}

	rank := int64(math.Ceil(product))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return rank
}

// addCounts accumulates b into a in place; a and b share the same histogram
// shape and therefore the same length.
func addCounts(a, b []int64) {
	for i, v := range b {
		a[i] += v
	}
}

func sumOf(a, b []int64) []int64 {
	sum := make([]int64, len(a))
	addCounts(sum, a)
	addCounts(sum, b)
	return sum
}

// withCounts returns a snapshot sharing shape with like but carrying counts.
func withCounts(like *hdrhistogram.Snapshot, counts []int64) *hdrhistogram.Snapshot {
	return &hdrhistogram.Snapshot{
		LowestTrackableValue:  like.LowestTrackableValue,
		HighestTrackableValue: like.HighestTrackableValue,
		SignificantFigures:    like.SignificantFigures,
		Counts:                counts,
	}
}

func cumulative(h *hdrhistogram.Histogram) []cumBar {
	var cum []cumBar
	var running int64
	for _, b := range h.Distribution() {
		if b.Count == 0 {
			continue
		}
		running += b.Count
		cum = append(cum, cumBar{upperNanos: b.To, cumCount: running})
	}
	return cum
}

// countAtOrBelow returns the count of observations in buckets whose upper
// bound does not exceed threshold: every one of them is guaranteed below any
// censored observation with a threshold of at least threshold, since a
// censored observation's true value always exceeds its own threshold.
func countAtOrBelow(cum []cumBar, threshold int64) int64 {
	idx := sort.Search(len(cum), func(i int) bool { return cum[i].upperNanos > threshold })
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
	return cum[idx].upperNanos
}
