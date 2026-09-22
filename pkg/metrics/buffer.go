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
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// Buffer is a copy of a distribution that is filled again and again without
// allocating, for a reader that looks several times a second. It gives the
// same percentiles as Snapshot. Its memory belongs to whoever made it: it is
// not safe for concurrent use, and two readers need two buffers.
type Buffer struct {
	measured *hdrhistogram.Histogram
	censored *hdrhistogram.Histogram
	// combined is measured plus censored, built only when a percentile lands
	// among the censored and kept until the next fill.
	combined      *hdrhistogram.Histogram
	combinedValid bool

	censoredMin int64
	invalidN    int64
}

func NewBuffer() *Buffer {
	return &Buffer{
		measured:    newHistogram(),
		censored:    newHistogram(),
		combined:    newHistogram(),
		censoredMin: math.MaxInt64,
	}
}

// CopyInto replaces what b holds with the current distribution. The copy is
// taken under the lock Record takes: it walks the recorded buckets, tens of
// microseconds, and recording waits all that time.
func (l *Latencies) CopyInto(b *Buffer) {
	b.reset()

	l.mu.Lock()
	// Merge re-records every bucket by its lowest value, which lands in the
	// same bucket of an identical histogram: the counts are copied exactly.
	b.measured.Merge(l.measured)
	b.censored.Merge(l.censored)
	l.mu.Unlock()

	b.invalidN = l.invalid.Load()
	if b.censored.TotalCount() > 0 {
		b.censoredMin = b.censored.Min()
	}
}

// MergeInto replaces what dst holds with the sum of sources.
func MergeInto(dst *Buffer, sources ...*Buffer) {
	dst.reset()

	for _, s := range sources {
		dst.measured.Merge(s.measured)
		dst.censored.Merge(s.censored)
		dst.invalidN += s.invalidN

		if s.censored.TotalCount() > 0 && s.censoredMin < dst.censoredMin {
			dst.censoredMin = s.censoredMin
		}
	}
}

func (b *Buffer) reset() {
	b.measured.Reset()
	b.censored.Reset()
	b.combinedValid = false
	b.censoredMin = math.MaxInt64
	b.invalidN = 0
}

// Count reports every observation, measured and censored alike.
func (b *Buffer) Count() int64 {
	return b.measured.TotalCount() + b.censored.TotalCount()
}

func (b *Buffer) CensoredCount() int64 {
	return b.censored.TotalCount()
}

func (b *Buffer) InvalidCount() int64 {
	return b.invalidN
}

// Percentile is Snapshot.Percentile over the buffer: p is a fraction in
// [0, 1], and outside it Percentile panics.
func (b *Buffer) Percentile(p float64) Quantile {
	if p < 0 || p > 1 {
		panic(fmt.Sprintf("metrics: Percentile(%v): p is a fraction in [0, 1], not a 0..100 scale", p))
	}

	n := b.Count()
	if n == 0 {
		return Quantile{}
	}

	rank := rankFor(p, n)

	// Exact when the rank-th measured value's bucket lies at or below every
	// censored threshold: then no censored observation can take that rank.
	// That is the same test as Snapshot's count at or below the smallest
	// threshold reaching the rank.
	if m := b.measured.TotalCount(); rank <= m {
		if v := valueAtRankOf(b.measured, rank); v <= b.censoredMin {
			return Quantile{Value: time.Duration(v), Exact: true, Defined: true}
		}
	}

	if !b.combinedValid {
		b.combined.Reset()
		b.combined.Merge(b.measured)
		b.combined.Merge(b.censored)
		b.combinedValid = true
	}

	return Quantile{Value: time.Duration(valueAtRankOf(b.combined, rank)), Defined: true}
}

// valueAtRankOf returns the upper bound of the bucket holding the rank-th
// observation of h. The library rounds its own count from a percentile as
// int64(q/100·total + 0.5); aiming at rank − 0.25 lands on rank whatever the
// float error, which is far below a quarter.
func valueAtRankOf(h *hdrhistogram.Histogram, rank int64) int64 {
	return h.ValueAtPercentile(100 * (float64(rank) - 0.25) / float64(h.TotalCount()))
}
