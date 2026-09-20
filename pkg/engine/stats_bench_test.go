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

package engine

import (
	"fmt"
	"testing"
	"time"
)

func benchStats(methods, perMethod int) *Stats {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for m := range methods {
		name := fmt.Sprintf("pkg.Service/Method%d", m)
		for i := range perMethod {
			stats.Record(Result{
				Method:      name,
				ScheduledAt: start,
				Outcome: Outcome{
					DoneAt:   start.Add(time.Duration(i%900_000) * time.Microsecond),
					Category: CategorySuccess,
				},
			})
		}
	}

	return stats
}

// BenchmarkStatsSnapshot measures what the interface pays per tick: it asks for
// a snapshot eight times a second, and the old implementation sorted every
// recorded latency under the recording lock each time.
func BenchmarkStatsSnapshot(b *testing.B) {
	stats := benchStats(10, 10000)

	b.ResetTimer()

	for range b.N {
		_ = stats.Snapshot()
	}
}

func BenchmarkStatsRecord(b *testing.B) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			stats.Record(Result{
				Method:      "pkg.Service/Method",
				ScheduledAt: start,
				Outcome:     Outcome{DoneAt: start.Add(12 * time.Millisecond), Category: CategorySuccess},
			})
		}
	})
}
