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
	"time"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

const (
	lowestTrackableMicros  = 1
	highestTrackableMicros = int64(time.Hour / time.Microsecond)
	significantFigures     = 3
)

// Latencies is the latency distribution of one method: measured values plus
// observations cut short by a timeout or by the run stopping.
//
// Safe for concurrent use.
type Latencies struct {
	mu       sync.Mutex
	measured *hdrhistogram.Histogram
	censored *hdrhistogram.Histogram
}

func NewLatencies() *Latencies {
	return &Latencies{
		measured: newHistogram(),
		censored: newHistogram(),
	}
}

func newHistogram() *hdrhistogram.Histogram {
	return hdrhistogram.New(lowestTrackableMicros, highestTrackableMicros, significantFigures)
}

func (l *Latencies) Record(d time.Duration) {
	micros := microsOf(d)

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.measured.RecordValue(micros); err != nil {
		l.recordCensoredLocked(highestTrackableMicros)
	}
}

// RecordCensored records an observation known only to be at least threshold:
// a request cut short by its timeout or by the run ending. Its true latency is
// larger by an unknown amount, so it is never stored as a measured value.
func (l *Latencies) RecordCensored(threshold time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.recordCensoredLocked(microsOf(threshold))
}

// recordCensoredLocked clamps micros into the histogram's range before
// recording it: a value above highestTrackableMicros cannot itself overflow,
// since the true reading is unknown beyond "at least this much" anyway.
func (l *Latencies) recordCensoredLocked(micros int64) {
	if micros > highestTrackableMicros {
		micros = highestTrackableMicros
	}
	_ = l.censored.RecordValue(micros)
}

func microsOf(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Microseconds()
}
