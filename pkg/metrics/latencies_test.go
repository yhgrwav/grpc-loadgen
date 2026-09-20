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
		micros := 20 + rng.Int64N(5_000_000)
		d := time.Duration(micros) * time.Microsecond
		samples[i] = d
		l.Record(d)
	}

	slices.Sort(samples)
	snap := l.Snapshot()

	for _, p := range []float64{0.5, 0.9, 0.99, 0.999} {
		want := referencePercentile(samples, p)
		got := snap.Percentile(p)
		if !got.Exact {
			t.Fatalf("p%v: expected exact result, got lower bound", p*100)
		}

		diff := got.Value - want
		if diff < 0 {
			diff = -diff
		}
		tolerance := time.Duration(float64(want)*0.005) + time.Microsecond
		if diff > tolerance {
			t.Errorf("p%v: histogram=%v reference=%v diff=%v exceeds tolerance %v",
				p*100, got.Value, want, diff, tolerance)
		}
	}
}

func referencePercentile(sorted []time.Duration, p float64) time.Duration {
	rank := int(math.Ceil(float64(len(sorted)) * p))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
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
	if q.Value < time.Hour || q.Value > time.Hour+time.Second {
		t.Errorf("lower bound = %v, want ~1h (the histogram ceiling, within bucket precision)", q.Value)
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
	if !q.Exact || q.Value != 0 {
		t.Errorf("Percentile(0.5) on empty snapshot = %+v, want {0 true}", q)
	}
}

func TestPercentileBoundaryFractions(t *testing.T) {
	l := NewLatencies()
	for i := 1; i <= 100; i++ {
		l.Record(time.Duration(i) * time.Millisecond)
	}
	snap := l.Snapshot()

	tests := []struct {
		name string
		p    float64
	}{
		{"p=0", 0},
		{"p=1", 1},
		{"p<0", -0.5},
		{"p>1", 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := snap.Percentile(tt.p)
			if !q.Exact {
				t.Errorf("Percentile(%v).Exact = false, want true", tt.p)
			}
			if q.Value <= 0 || q.Value > 101*time.Millisecond {
				t.Errorf("Percentile(%v) = %v, want in (0, ~100ms]", tt.p, q.Value)
			}
		})
	}
}
