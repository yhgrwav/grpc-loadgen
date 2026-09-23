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
	"testing"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// bothPercentiles reads p99 from a Snapshot and from a Buffer of the same
// distribution: the live view and the report must agree.
func bothPercentiles(t *testing.T, l *Latencies) (snap, buf Quantile) {
	t.Helper()

	b := NewBuffer()
	l.CopyInto(b)

	return l.Snapshot().Percentile(0.99), b.Percentile(0.99)
}

func record(l *Latencies, n int, d time.Duration) {
	for range n {
		l.Record(d)
	}
}

func recordCensored(l *Latencies, n int, threshold time.Duration) {
	for range n {
		l.RecordCensored(threshold)
	}
}

// Ground: contract — when every censored observation has the same threshold
// c and fewer than r measured ones can be below c, the r-th value is at least
// c: the bound is c itself, not the edge of c's bucket. A timeout of 1995ms
// read at its bucket's top claims more than is known.
func TestBoundIsTheThresholdWhenAllCensoredShareIt(t *testing.T) {
	for _, threshold := range []time.Duration{1995 * time.Millisecond, 999_600 * time.Microsecond, 5 * time.Second} {
		l := NewLatencies()
		record(l, 90, 10*time.Millisecond)
		recordCensored(l, 10, threshold)

		snap, buf := bothPercentiles(t, l)
		for name, q := range map[string]Quantile{"snapshot": snap, "buffer": buf} {
			if q.Exact || q.Value != threshold {
				t.Errorf("%s: timeout %v: p99 = %+v, want the bound %v itself", name, threshold, q, threshold)
			}
		}
	}
}

// Ground: contract — with thresholds of 5s and 3.2s (an abort cut some calls
// short) no single threshold is the bound. A censored call's true value is at
// least its own threshold, so the k-th smallest true value is at least the
// k-th smallest recorded value: p99, the 9th of the ten censored, is past the
// bottom of the 5s bucket (not 5s itself: the thresholds differ), and p95,
// landing on the 3.2s ones, claims no more than 3.2s.
func TestBoundWithMixedThresholdsIsTheBottomOfTheRanksBucket(t *testing.T) {
	l := NewLatencies()
	record(l, 90, 10*time.Millisecond)
	recordCensored(l, 5, 5*time.Second)
	recordCensored(l, 5, 3200*time.Millisecond)

	b := NewBuffer()
	l.CopyInto(b)
	snap := l.Snapshot()
	bottom := bucketBottom(5 * time.Second)

	for name, q := range map[string]Quantile{"snapshot": snap.Percentile(0.99), "buffer": b.Percentile(0.99)} {
		if q.Exact || q.Value != bottom {
			t.Errorf("%s: p99 = %+v, want the bottom of the 5s bucket, %v", name, q, bottom)
		}
	}
	for name, q := range map[string]Quantile{"snapshot": snap.Percentile(0.95), "buffer": b.Percentile(0.95)} {
		if q.Exact || q.Value > 3200*time.Millisecond {
			t.Errorf("%s: p95 = %+v, want a bound of at most 3.2s", name, q)
		}
	}
}

// bucketBottom is the lowest value the histograms store in d's bucket.
func bucketBottom(d time.Duration) time.Duration {
	h := hdrhistogram.New(lowestTrackableNanos, highestTrackableNanos, significantFigures)
	_ = h.RecordValue(int64(d))

	return time.Duration(h.Min())
}

// Ground: boundary — a measured call in the same bucket as the threshold may
// be below it, so it counts against the rank: here it makes 99 calls that may
// be below 5s, not fewer than the rank, and the bound falls back to the
// bottom of the bucket rather than claiming 5s.
func TestBoundCountsAMeasuredCallInTheThresholdsBucketAsBelowIt(t *testing.T) {
	l := NewLatencies()
	record(l, 98, 10*time.Millisecond)
	record(l, 1, 5002*time.Millisecond)
	recordCensored(l, 1, 5*time.Second)

	bottom := bucketBottom(5 * time.Second)

	snap, buf := bothPercentiles(t, l)
	for name, q := range map[string]Quantile{"snapshot": snap, "buffer": buf} {
		if q.Exact || q.Value != bottom {
			t.Errorf("%s: p99 = %+v, want the bottom of the 5s bucket, %v", name, q, bottom)
		}
	}
}

// Ground: contract — merging keeps the thresholds exact: two parts censored at
// the same c merge to a bound of c; parts censored at 5s and 3.2s no longer
// share one, and the bound is the bottom of the rank's bucket. Both the
// report's Merge and the live view's MergeInto.
func TestMergeKeepsTheSmallestAndLargestThreshold(t *testing.T) {
	part := func(threshold time.Duration) *Latencies {
		l := NewLatencies()
		record(l, 45, 10*time.Millisecond)
		recordCensored(l, 5, threshold)

		return l
	}
	merged := func(a, b *Latencies) (snap, buf Quantile) {
		ba, bb, dst := NewBuffer(), NewBuffer(), NewBuffer()
		a.CopyInto(ba)
		b.CopyInto(bb)
		MergeInto(dst, ba, bb)

		return Merge(a.Snapshot(), b.Snapshot()).Percentile(0.99), dst.Percentile(0.99)
	}

	snap, buf := merged(part(1995*time.Millisecond), part(1995*time.Millisecond))
	for name, q := range map[string]Quantile{"snapshot": snap, "buffer": buf} {
		if q.Value != 1995*time.Millisecond {
			t.Errorf("%s: same threshold merged: p99 = %+v, want 1.995s", name, q)
		}
	}

	// Thresholds of 5s and 3.2s merged: no longer one threshold, so the
	// bound is the bottom of the rank's bucket, 5s's here, not 5s itself.
	snap, buf = merged(part(5*time.Second), part(3200*time.Millisecond))
	for name, q := range map[string]Quantile{"snapshot": snap, "buffer": buf} {
		if q.Value != bucketBottom(5*time.Second) {
			t.Errorf("%s: 5s and 3.2s merged: p99 = %+v, want the bottom of the 5s bucket", name, q)
		}
	}
}
