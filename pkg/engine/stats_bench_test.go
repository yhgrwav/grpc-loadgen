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
	"sync"
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
	for _, perMethod := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("obs%d", perMethod), func(b *testing.B) {
			stats := benchStats(10, perMethod)

			b.ResetTimer()

			for range b.N {
				_ = stats.Snapshot()
			}
		})
	}
}

// BenchmarkStatsRecord runs at the concurrency a real run reaches: one
// goroutine per in-flight request, which at 1000 RPS against a target
// answering in 500ms means five hundred of them writing to one distribution.
func BenchmarkStatsRecord(b *testing.B) {
	for _, writers := range []int{8, 100, 500, 1000} {
		b.Run(fmt.Sprintf("writers%d", writers), func(b *testing.B) {
			stats := NewStats()
			start := time.Now()
			stats.Start(start, 0)

			var wg sync.WaitGroup

			each := b.N / writers
			if each < 1 {
				each = 1
			}

			b.ResetTimer()

			for range writers {
				wg.Add(1)

				go func() {
					defer wg.Done()

					for range each {
						stats.Record(Result{
							Method:      "pkg.Service/Method",
							ScheduledAt: start,
							Outcome:     Outcome{DoneAt: start.Add(12 * time.Millisecond), Category: CategorySuccess},
						})
					}
				}()
			}

			wg.Wait()
			b.StopTimer()
			b.ReportMetric(float64(writers*each), "records")
		})
	}
}
