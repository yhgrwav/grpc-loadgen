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
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"
)

// fraction is a percentile given as an exact ratio, so the reference rank can
// be computed with integer arithmetic instead of float64, which is the very
// thing under test.
type fraction struct {
	num, den int64
}

func (f fraction) float() float64 { return float64(f.num) / float64(f.den) }

func exactRank(n int64, f fraction) int64 {
	rank := (f.num*n + f.den - 1) / f.den
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return rank
}

var referenceFractions = []fraction{{50, 100}, {90, 100}, {99, 100}, {999, 1000}}

// TestPercentileAgainstReference is the main defense against a histogram that
// lies quietly: it computes p50/p90/p99/p999 two ways — through the
// histogram and by sorting the raw sample — on a random, seeded dataset, and
// requires them to agree within the library's documented three-significant-
// figure accuracy. It catches three different failure modes with one test:
// our misuse of the library, a bug in the library itself, and a regression
// on a version bump.
func TestPercentileAgainstReference(t *testing.T) {
	const n = 10000
	rng := rand.New(rand.NewPCG(1, 2))

	samples := make([]time.Duration, n)
	l := NewLatencies()
	for i := range samples {
		nanos := 20_000 + rng.Int64N(5_000_000_000)
		d := time.Duration(nanos)
		samples[i] = d
		l.Record(d)
	}

	slices.Sort(samples)
	snap := l.Snapshot()

	for _, f := range referenceFractions {
		rank := exactRank(n, f)
		want := samples[rank-1]
		got := snap.Percentile(f.float())
		if !got.Exact {
			t.Fatalf("p%v/%v: expected exact result, got lower bound", f.num, f.den)
		}
		assertUpperBound(t, f, got.Value, want)
	}
}

// assertUpperBound checks the one-sided tolerance a bucketed histogram
// actually gives: it always rounds a rank up to its bucket's upper edge, so
// the reported value can only be >= the true one, by at most the bucket
// width at three significant figures.
func assertUpperBound(t *testing.T, f fraction, got, want time.Duration) {
	t.Helper()
	tolerance := want/1024 + time.Nanosecond
	if got < want || got > want+tolerance {
		t.Errorf("p%v/%v: histogram=%v reference=%v, want in [%v, %v]",
			f.num, f.den, got, want, want, want+tolerance)
	}
}

// TestPercentileAgainstReferenceWithCensoredMix runs the same cross-check as
// TestPercentileAgainstReference over a mix of measured and censored
// observations, so the exactness criterion is verified against a reference
// and not just against hand-picked numbers.
func TestPercentileAgainstReferenceWithCensoredMix(t *testing.T) {
	const n = 5000
	rng := rand.New(rand.NewPCG(7, 11))

	l := NewLatencies()
	var measured, combined []int64
	var thresholds []int64

	for i := 0; i < n; i++ {
		nanos := 1_000 + rng.Int64N(500_000_000)
		if rng.Int64N(5) == 0 {
			l.RecordCensored(time.Duration(nanos))
			thresholds = append(thresholds, nanos)
		} else {
			l.Record(time.Duration(nanos))
			measured = append(measured, nanos)
		}
		combined = append(combined, nanos)
	}
	slices.Sort(measured)
	slices.Sort(combined)

	censoredMin := int64(math.MaxInt64)
	if len(thresholds) > 0 {
		censoredMin = slices.Min(thresholds)
	}

	snap := l.Snapshot()

	for _, f := range referenceFractions {
		rank := exactRank(n, f)
		wantExact := rank <= int64(len(measured)) && measured[rank-1] < censoredMin

		var want time.Duration
		if wantExact {
			want = time.Duration(measured[rank-1])
		} else {
			want = time.Duration(combined[rank-1])
		}

		got := snap.Percentile(f.float())
		if got.Exact != wantExact {
			t.Fatalf("p%v/%v: Exact = %v, want %v", f.num, f.den, got.Exact, wantExact)
		}
		assertUpperBound(t, f, got.Value, want)
	}
}

// TestPercentileExactnessBoundary constructs a rank that lands exactly on the
// number of measured observations below the smallest censored threshold, and
// one rank above it, to pin down that the criterion is "<=", not "<".
func TestPercentileExactnessBoundary(t *testing.T) {
	l := NewLatencies()
	for i := 1; i <= 100; i++ {
		l.Record(time.Duration(i) * time.Millisecond)
	}
	l.RecordCensored(50*time.Millisecond + 500*time.Microsecond)

	snap := l.Snapshot()

	// n = 101, p = 50/101 gives rank 50: the 50th measured value (50ms) is
	// the last one strictly below the censored threshold — last exact rank.
	last := snap.Percentile(fraction{50, 101}.float())
	if !last.Exact {
		t.Errorf("rank at the boundary: Exact = false, want true")
	}

	// p = 51/101 gives rank 51: the censored observation could take this
	// rank — first inexact rank.
	first := snap.Percentile(fraction{51, 101}.float())
	if first.Exact {
		t.Errorf("rank past the boundary: Exact = true, want false")
	}
}

func TestPercentileExactWithoutCensored(t *testing.T) {
	l := NewLatencies()
	for i := 1; i <= 1000; i++ {
		l.Record(time.Duration(i) * time.Millisecond)
	}

	snap := l.Snapshot()
	q := snap.Percentile(0.99)
	if !q.Exact {
		t.Fatalf("expected exact p99, got lower bound %v", q.Value)
	}
	if q.Value < 985*time.Millisecond || q.Value > 995*time.Millisecond {
		t.Errorf("p99 = %v, want close to 990ms", q.Value)
	}
}

func TestPercentileExactWhenCensoredAboveRank(t *testing.T) {
	l := NewLatencies()
	for i := 0; i < 10000; i++ {
		l.Record(10 * time.Millisecond)
	}
	for i := 0; i < 50; i++ {
		l.RecordCensored(time.Second)
	}

	snap := l.Snapshot()
	q := snap.Percentile(0.99)
	if !q.Exact {
		t.Fatalf("expected exact p99 (censored observations rank above it), got lower bound %v", q.Value)
	}
	if q.Value < 9*time.Millisecond || q.Value > 11*time.Millisecond {
		t.Errorf("p99 = %v, want close to 10ms", q.Value)
	}
}

func TestPercentileLowerBoundWhenCensoredAtRank(t *testing.T) {
	l := NewLatencies()
	for i := 0; i < 10000; i++ {
		l.Record(10 * time.Millisecond)
	}
	for i := 0; i < 300; i++ {
		l.RecordCensored(time.Second)
	}

	snap := l.Snapshot()
	q := snap.Percentile(0.99)
	if q.Exact {
		t.Fatalf("expected p99 to fall into the censored zone, got exact %v", q.Value)
	}
	if q.Value < time.Second {
		t.Errorf("p99 lower bound = %v, want >= 1s", q.Value)
	}
}

// TestPercentileLowerBoundStrongerThanMinThreshold constructs a case where
// the union-based lower bound is strictly above the smallest individual
// censored threshold, proving countAtOrBelow/valueAtRank use the union and
// not just censored.min().
func TestPercentileLowerBoundStrongerThanMinThreshold(t *testing.T) {
	l := NewLatencies()
	for i := 0; i < 10; i++ {
		l.Record(time.Millisecond)
	}
	l.RecordCensored(500 * time.Millisecond)
	l.RecordCensored(600 * time.Millisecond)
	l.RecordCensored(700 * time.Millisecond)

	// N = 13, p wants rank 13 (the max): must land in the censored region,
	// and the union's valueAtRank must reach the highest threshold (700ms),
	// not stop at the minimum censored threshold (500ms).
	snap := l.Snapshot()
	q := snap.Percentile(1.0)
	if q.Exact {
		t.Fatalf("expected lower bound, got exact %v", q.Value)
	}
	if q.Value <= 500*time.Millisecond {
		t.Errorf("lower bound = %v, want strictly above the minimum censored threshold of 500ms", q.Value)
	}
}

func TestRecordAboveRangeBecomesCensored(t *testing.T) {
	l := NewLatencies()
	l.Record(2 * time.Hour)

	snap := l.Snapshot()
	if snap.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", snap.Count())
	}
	if snap.CensoredCount() != 1 {
		t.Fatalf("CensoredCount() = %d, want 1", snap.CensoredCount())
	}

	q := snap.Percentile(1.0)
	if q.Exact {
		t.Fatalf("expected lower bound for an out-of-range observation, got exact %v", q.Value)
	}
	if q.Value < time.Hour || q.Value > time.Hour+time.Hour/1024 {
		t.Errorf("lower bound = %v, want ~1h (the histogram ceiling, within bucket precision)", q.Value)
	}
}

func TestRecordNegativeDurationCountsAsInvalid(t *testing.T) {
	l := NewLatencies()
	l.Record(-time.Millisecond)
	l.Record(time.Millisecond)

	snap := l.Snapshot()
	if snap.InvalidCount() != 1 {
		t.Errorf("InvalidCount() = %d, want 1", snap.InvalidCount())
	}
	if snap.Count() != 1 {
		t.Errorf("Count() = %d, want 1 (the negative duration must not be recorded)", snap.Count())
	}
}

func TestRecordCensoredNegativeThresholdCountsAsInvalid(t *testing.T) {
	l := NewLatencies()
	l.RecordCensored(-time.Second)

	snap := l.Snapshot()
	if snap.InvalidCount() != 1 {
		t.Errorf("InvalidCount() = %d, want 1", snap.InvalidCount())
	}
	if snap.Count() != 0 {
		t.Errorf("Count() = %d, want 0", snap.Count())
	}
}

func TestConcurrentRecordDoesNotLoseObservations(t *testing.T) {
	l := NewLatencies()
	const goroutines = 8
	const perGoroutine = 5000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed))
			for i := 0; i < perGoroutine; i++ {
				if i%97 == 0 {
					l.RecordCensored(time.Duration(rng.Int64N(1000)) * time.Millisecond)
					continue
				}
				l.Record(time.Duration(rng.Int64N(1_000_000)) * time.Microsecond)
			}
		}(uint64(g) + 1)
	}
	wg.Wait()

	snap := l.Snapshot()
	if want := int64(goroutines * perGoroutine); snap.Count() != want {
		t.Errorf("Count() = %d, want %d", snap.Count(), want)
	}
}

func TestPercentileEmptySnapshot(t *testing.T) {
	snap := NewLatencies().Snapshot()
	if snap.Count() != 0 {
		t.Fatalf("Count() = %d, want 0", snap.Count())
	}
	q := snap.Percentile(0.5)
	if q.Defined {
		t.Errorf("Percentile(0.5) on empty snapshot: Defined = true, want false")
	}
}

func TestPercentileBoundaryFractions(t *testing.T) {
	l := NewLatencies()
	for i := 1; i <= 100; i++ {
		l.Record(time.Duration(i) * time.Millisecond)
	}
	snap := l.Snapshot()

	for _, p := range []float64{0, 1} {
		q := snap.Percentile(p)
		if !q.Defined || !q.Exact {
			t.Errorf("Percentile(%v) = %+v, want defined and exact", p, q)
		}
		if q.Value <= 0 || q.Value > 101*time.Millisecond {
			t.Errorf("Percentile(%v) = %v, want in (0, ~100ms]", p, q.Value)
		}
	}

	for _, p := range []float64{-0.5, 1.5, 99} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Percentile(%v) did not panic, want a panic for a p outside [0, 1]", p)
				}
			}()
			snap.Percentile(p)
		}()
	}
}

func TestRankForMatchesIntegerArithmetic(t *testing.T) {
	for _, n := range []int64{10, 50, 100, 1000, 10000, 12345} {
		for num := int64(1); num < 1000; num++ {
			const den = int64(1000)
			want := (num*n + den - 1) / den
			if want < 1 {
				want = 1
			}
			if want > n {
				want = n
			}
			if got := rankFor(float64(num)/float64(den), n); got != want {
				t.Fatalf("rankFor(%d/%d, %d) = %d, want %d", num, den, n, got, want)
			}
		}
	}
}

func TestMergeMatchesSingleDistribution(t *testing.T) {
	a := NewLatencies()
	b := NewLatencies()
	combined := NewLatencies()

	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 2000; i++ {
		d := time.Duration(1+rng.Int64N(2_000_000)) * time.Microsecond
		a.Record(d)
		combined.Record(d)
	}
	for i := 0; i < 500; i++ {
		d := time.Duration(1+rng.Int64N(2_000_000)) * time.Microsecond
		b.RecordCensored(d)
		combined.RecordCensored(d)
	}

	merged := Merge(a.Snapshot(), b.Snapshot())
	want := combined.Snapshot()

	if merged.Count() != want.Count() {
		t.Fatalf("Count() = %d, want %d", merged.Count(), want.Count())
	}
	if merged.CensoredCount() != want.CensoredCount() {
		t.Fatalf("CensoredCount() = %d, want %d", merged.CensoredCount(), want.CensoredCount())
	}

	for _, f := range referenceFractions {
		got := merged.Percentile(f.float())
		wantQ := want.Percentile(f.float())
		if got != wantQ {
			t.Errorf("p%v/%v: merged=%+v, want %+v", f.num, f.den, got, wantQ)
		}
	}
}

func TestMergeOfNoSnapshotsIsEmpty(t *testing.T) {
	merged := Merge()
	if merged.Count() != 0 {
		t.Fatalf("Count() = %d, want 0", merged.Count())
	}
	if q := merged.Percentile(0.5); q.Defined {
		t.Errorf("Percentile(0.5) = %+v, want Defined = false", q)
	}
}

func TestPercentileZeroOnEmptySnapshotIsUndefined(t *testing.T) {
	snap := NewLatencies().Snapshot()

	for _, p := range []float64{0, 0.5, 1} {
		got := snap.Percentile(p)
		if got.Defined {
			t.Errorf("Percentile(%v) = %+v, want undefined: a zero here would be "+
				"indistinguishable from a measured zero", p, got)
		}
	}
}
