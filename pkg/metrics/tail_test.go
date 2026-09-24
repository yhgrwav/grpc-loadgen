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
)

// Ground: contract — Snapshot.CountAtOrAbove is public; the tail of p99 is the
// calls at or above it, censored ones included.
func TestSnapshot_CountAtOrAboveCountsTheTailOfAPercentile(t *testing.T) {
	l := NewLatencies()
	for range 97 {
		l.Record(2 * time.Millisecond)
	}
	l.Record(52 * time.Millisecond)
	l.Record(52 * time.Millisecond)
	l.RecordCensored(time.Second)
	s := l.Snapshot()

	p99 := s.Percentile(0.99)
	for _, c := range []struct {
		at   time.Duration
		want int64
	}{
		{p99.Value, 3},
		{time.Millisecond, 100},
		{2 * time.Millisecond, 100},
		{3 * time.Millisecond, 3},
		{2 * time.Second, 0},
	} {
		if got := s.CountAtOrAbove(c.at); got != c.want {
			t.Errorf("CountAtOrAbove(%v) = %d, want %d (p99 %v)", c.at, got, c.want, p99.Value)
		}
	}
}
