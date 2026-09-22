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
	"sync"
	"sync/atomic"
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

const (
	lowestTrackableNanos  = 1
	highestTrackableNanos = int64(time.Hour)
	significantFigures    = 3
)

// Latencies is the latency distribution of one method: measured values plus
// observations cut short by a timeout or by the run stopping.
//
// Safe for concurrent use.
type Latencies struct {
	mu       sync.Mutex
	measured *hdrhistogram.Histogram
	// censored is nil in an uncensored distribution until a value past the
	// range needs it.
	censored *hdrhistogram.Histogram
	invalid  atomic.Int64
}

func NewLatencies() *Latencies {
	return &Latencies{
		measured: newHistogram(),
		censored: newHistogram(),
	}
}

// NewUncensoredLatencies is for a quantity never cut short, such as how long
// a target took to refuse. It skips the histogram of lower bounds, half the
// memory, and creates it only if a value past the range turns up.
func NewUncensoredLatencies() *Latencies {
	return &Latencies{measured: newHistogram()}
}

func newHistogram() *hdrhistogram.Histogram {
	return hdrhistogram.New(lowestTrackableNanos, highestTrackableNanos, significantFigures)
}

// Record stores a measured latency. A negative duration is a caller bug, not
// data: it is counted separately rather than silently folded into zero.
func (l *Latencies) Record(d time.Duration) {
	if d < 0 {
		l.invalid.Add(1)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.measured.RecordValue(d.Nanoseconds()); err != nil {
		l.recordCensoredLocked(highestTrackableNanos)
	}
}

// RecordCensored records an observation known only to be at least threshold:
// a request cut short by its timeout or by the run ending. Its true latency is
// larger by an unknown amount, so it is never stored as a measured value.
func (l *Latencies) RecordCensored(threshold time.Duration) {
	if threshold < 0 {
		l.invalid.Add(1)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.recordCensoredLocked(threshold.Nanoseconds())
}

// recordCensoredLocked clamps nanos into the histogram's range before
// recording it: a value above highestTrackableNanos cannot itself overflow,
// since the true reading is unknown beyond "at least this much" anyway.
func (l *Latencies) recordCensoredLocked(nanos int64) {
	if nanos > highestTrackableNanos {
		nanos = highestTrackableNanos
	}
	if l.censored == nil {
		l.censored = newHistogram()
	}
	_ = l.censored.RecordValue(nanos)
}
