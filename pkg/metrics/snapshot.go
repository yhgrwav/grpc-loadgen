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
	"math/bits"
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
	censoredMax int64
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
	censoredRaw := emptyExport()
	if l.censored != nil {
		censoredRaw = l.censored.Export()
	}
	censoredMin, censoredMax := l.censoredMin, l.censoredMax
	l.mu.Unlock()

	return &Snapshot{
		measuredRaw: measuredRaw,
		censoredRaw: censoredRaw,
		measuredN:   hdrhistogram.Import(measuredRaw).TotalCount(),
		censoredN:   hdrhistogram.Import(censoredRaw).TotalCount(),
		censoredMin: censoredMin,
		censoredMax: censoredMax,
		invalidN:    l.invalid.Load(),
	}
}

// emptyExport stands in for the censored histogram an uncensored distribution
// does not have. Shared and never written: Merge and Import copy what they read.
var emptyExport = sync.OnceValue(func() *hdrhistogram.Snapshot { return newHistogram().Export() })

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
	return percentile(s, p)
}

func (s *Snapshot) counts() (measured, censored int64) { return s.measuredN, s.censoredN }
func (s *Snapshot) thresholds() (lo, hi int64)         { return s.censoredMin, s.censoredMax }
func (s *Snapshot) measuredAt(rank int64) int64        { return valueAtRank(s.measuredCumBars(), rank) }
func (s *Snapshot) combinedAt(rank int64) int64        { return valueAtRank(s.combinedCumBars(), rank) }

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
		if s.censoredN > 0 {
			merged.censoredMin = min(merged.censoredMin, s.censoredMin)
			merged.censoredMax = max(merged.censoredMax, s.censoredMax)
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

// lowestEquivalent is the smallest value stored in v's bucket. With a lowest
// trackable value of 1 and 3 significant figures the histograms keep 2048
// sub-buckets: below 2048 a bucket is one value, above it 2^(bits(v)-11).
func lowestEquivalent(v int64) int64 {
	shift := max(0, bits.Len64(uint64(v))-11)

	return v >> shift << shift
}

// highestEquivalent is the largest value stored in v's bucket.
func highestEquivalent(v int64) int64 {
	shift := max(0, bits.Len64(uint64(v))-11)

	return lowestEquivalent(v) + 1<<shift - 1
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
