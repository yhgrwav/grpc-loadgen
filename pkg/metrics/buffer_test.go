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
	"math/rand/v2"
	"runtime"
	"testing"
	"time"
)

// fill records into l what the named case describes.
type distribution struct {
	name string
	fill func(l *Latencies)
}

func distributions() []distribution {
	return []distribution{
		{"empty", func(*Latencies) {}},
		{"one point", func(l *Latencies) { l.Record(3 * time.Millisecond) }},
		{"only censored", func(l *Latencies) {
			for i := range 50 {
				l.RecordCensored(time.Second + time.Duration(i)*time.Millisecond)
			}
		}},
		{"mix, censored above most", func(l *Latencies) {
			r := rand.New(rand.NewPCG(1, 2))
			for range 5000 {
				l.Record(time.Duration(r.Int64N(int64(800 * time.Millisecond))))
			}
			for range 300 {
				l.RecordCensored(time.Second)
			}
		}},
		{"mix, censored low", func(l *Latencies) {
			r := rand.New(rand.NewPCG(3, 4))
			for range 2000 {
				l.Record(time.Duration(r.Int64N(int64(2 * time.Second))))
			}
			for range 700 {
				l.RecordCensored(100 * time.Millisecond)
			}
		}},
		{"bucket bounds", func(l *Latencies) {
			// Powers of two and their neighbours sit on the edges of HDR buckets.
			for shift := range 40 {
				v := int64(1) << shift
				for _, d := range []int64{v - 1, v, v + 1} {
					if d > 0 {
						l.Record(time.Duration(d))
					}
				}
			}
		}},
		{"censored threshold equal to the rank's bucket", func(l *Latencies) {
			// Below 2048ns a bucket is one nanosecond wide, so a measured
			// value can sit exactly on the smallest censored threshold. It is
			// at or below it: the percentile there is exact.
			for range 10 {
				l.Record(5)
			}
			for range 10 {
				l.RecordCensored(5)
			}
		}},
		{"above range", func(l *Latencies) {
			l.Record(2 * time.Hour)
			l.Record(time.Millisecond)
		}},
	}
}

var fractions = []float64{0, 0.001, 0.25, 0.5, 0.9, 0.95, 0.99, 0.999, 1}

// Ground: signal hdrhistogram-go v1.3.0 — Buffer computes percentiles from bucket counts itself,
// repeating the library's undocumented ValueAtQuantile formula; an upgrade that changes it goes red
// here instead of splitting live and report numbers. The allocating Snapshot is the reference,
// checked against exact percentiles in latencies_test.go.
func TestBuffer_PercentilesMatchTheSnapshot(t *testing.T) {
	for _, d := range distributions() {
		t.Run(d.name, func(t *testing.T) {
			l := NewLatencies()
			d.fill(l)

			want := l.Snapshot()
			buf := NewBuffer()
			l.CopyInto(buf)

			if buf.Count() != want.Count() || buf.CensoredCount() != want.CensoredCount() {
				t.Errorf("count %d censored %d, want %d and %d",
					buf.Count(), buf.CensoredCount(), want.Count(), want.CensoredCount())
			}

			for _, p := range fractions {
				if got, w := buf.Percentile(p), want.Percentile(p); got != w {
					t.Errorf("p%v = %+v, want %+v", p, got, w)
				}
			}
		})
	}
}

// Ground: signal hdrhistogram-go v1.3.0 — as above, for the merge of several methods.
func TestBuffer_MergeMatchesTheSnapshotMerge(t *testing.T) {
	var snapshots []*Snapshot
	var buffers []*Buffer

	for _, d := range distributions() {
		l := NewLatencies()
		d.fill(l)
		snapshots = append(snapshots, l.Snapshot())

		buf := NewBuffer()
		l.CopyInto(buf)
		buffers = append(buffers, buf)
	}

	want := Merge(snapshots...)
	merged := NewBuffer()
	MergeInto(merged, buffers...)

	for _, p := range fractions {
		if got, w := merged.Percentile(p), want.Percentile(p); got != w {
			t.Errorf("p%v = %+v, want %+v", p, got, w)
		}
	}

	// Merging again into the same buffer starts from scratch.
	MergeInto(merged, buffers...)
	if merged.Count() != want.Count() {
		t.Errorf("count after a second merge = %d, want %d", merged.Count(), want.Count())
	}
}

// Ground: contract — a buffer reused for the next copy holds
// only the new distribution.
func TestBuffer_CopyReplacesWhatWasThere(t *testing.T) {
	buf := NewBuffer()

	big := NewLatencies()
	for range 100 {
		big.Record(time.Second)
	}
	big.RecordCensored(time.Minute)
	big.CopyInto(buf)

	small := NewLatencies()
	small.Record(time.Millisecond)
	small.CopyInto(buf)

	if buf.Count() != 1 || buf.CensoredCount() != 0 {
		t.Errorf("count %d censored %d, want 1 and 0", buf.Count(), buf.CensoredCount())
	}
	if got := buf.Percentile(1); !got.Exact || got.Value > 2*time.Millisecond {
		t.Errorf("max = %+v, want about 1ms, exact", got)
	}
}

// Ground: contract — a percentile among the censored builds a
// combined distribution; the next fill must not reuse it.
func TestBuffer_RefillDropsTheCombinedDistribution(t *testing.T) {
	buf := NewBuffer()

	for _, d := range distributions() {
		l := NewLatencies()
		d.fill(l)
		l.CopyInto(buf)

		want := l.Snapshot()
		for _, p := range fractions {
			if got, w := buf.Percentile(p), want.Percentile(p); got != w {
				t.Errorf("%s after refill: p%v = %+v, want %+v", d.name, p, got, w)
			}
		}
	}
}

// Ground: hot path — the live view takes a copy several times a second; an allocation there shows
// up as generator lag.
func TestBuffer_RepeatedUseDoesNotAllocate(t *testing.T) {
	l := NewLatencies()
	for i := range 1000 {
		l.Record(time.Duration(i) * time.Microsecond)
	}
	l.RecordCensored(time.Second)

	buf, merged := NewBuffer(), NewBuffer()
	others := make([]*Buffer, 10)
	for i := range others {
		others[i] = NewBuffer()
		l.CopyInto(others[i])
	}

	use := func() {
		l.CopyInto(buf)
		MergeInto(merged, others...)
		for _, p := range fractions {
			_ = buf.Percentile(p)
			_ = merged.Percentile(p)
		}
	}
	use()

	if allocs := testing.AllocsPerRun(20, use); allocs != 0 {
		t.Errorf("a repeated copy, merge and percentiles allocate %v times, want 0", allocs)
	}
}

func ExampleBuffer() {
	l := NewLatencies()
	l.Record(10 * time.Millisecond)

	buf := NewBuffer()
	l.CopyInto(buf)
	fmt.Println(buf.Percentile(0.5).Value.Round(time.Millisecond))
	// Output: 10ms
}

// Ground: contract — refusals are never cut short, so their
// distribution carries no censored histogram until a value above its range
// needs one.
func TestUncensored_WorksLikeLatenciesWithoutTheSecondHistogram(t *testing.T) {
	empty := NewUncensoredLatencies()
	empty.Record(time.Millisecond)
	emptyBuf := NewBuffer()
	empty.CopyInto(emptyBuf)
	for name, got := range map[string]Quantile{
		"snapshot": empty.Snapshot().Percentile(0.5), "buffer": emptyBuf.Percentile(0.5),
	} {
		if !got.Exact || got.Value > 2*time.Millisecond {
			t.Errorf("%s p50 = %+v, want about 1ms, exact", name, got)
		}
	}

	l := NewUncensoredLatencies()
	l.Record(2 * time.Millisecond)
	l.Record(2 * time.Hour)

	buf := NewBuffer()
	l.CopyInto(buf)

	for name, got := range map[string]interface {
		Count() int64
		CensoredCount() int64
	}{"snapshot": l.Snapshot(), "buffer": buf} {
		if got.Count() != 2 || got.CensoredCount() != 1 {
			t.Errorf("%s: count %d censored %d, want 2 and 1: past the range is a lower bound",
				name, got.Count(), got.CensoredCount())
		}
	}

	if merged := Merge(empty.Snapshot(), l.Snapshot()); merged.Count() != 3 {
		t.Errorf("merged count = %d, want 3", merged.Count())
	}
}

// Ground: hot path — the point of the constructor is memory.
func TestUncensored_TakesHalfTheMemory(t *testing.T) {
	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)
	full := NewLatencies()
	runtime.ReadMemStats(&after)
	fullBytes := after.TotalAlloc - before.TotalAlloc

	runtime.ReadMemStats(&before)
	half := NewUncensoredLatencies()
	runtime.ReadMemStats(&after)
	halfBytes := after.TotalAlloc - before.TotalAlloc

	runtime.KeepAlive(full)
	runtime.KeepAlive(half)

	if halfBytes*3 > fullBytes*2 {
		t.Errorf("uncensored takes %d bytes, the full one %d: want about half", halfBytes, fullBytes)
	}
}
