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
	"slices"
	"strings"
	"sync"
	"time"
)

type Snapshot struct {
	Elapsed  time.Duration
	Total    time.Duration
	Sent     int
	Failed   int
	InFlight int
	RPS      float64
	P50      time.Duration
	P90      time.Duration
	P99      time.Duration
	Methods  []MethodSnapshot
}

type MethodSnapshot struct {
	Method    string
	Sent      int
	Failed    int
	RPS       float64
	TargetRPS int
	P50       time.Duration
	P90       time.Duration
	P99       time.Duration
}

type MethodReport struct {
	Method    string
	Sent      int
	Failed    int
	RPS       float64
	Min       time.Duration
	P50       time.Duration
	P90       time.Duration
	P95       time.Duration
	P99       time.Duration
	Max       time.Duration
	Latencies int
}

type Report struct {
	Duration time.Duration
	Sent     int
	Failed   int
	Methods  []MethodReport
}

type Stats struct {
	mu        sync.Mutex
	startedAt time.Time
	endedAt   time.Time
	warmup    time.Duration
	sent      int
	failed    int
	byMethod  map[string]*methodStats
}

type methodStats struct {
	sent      int
	failed    int
	latencies []time.Duration
}

func NewStats() *Stats {
	return &Stats{byMethod: make(map[string]*methodStats)}
}

func (s *Stats) Start(at time.Time, warmup time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.startedAt = at
	s.warmup = warmup
}

func (s *Stats) Finish(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.endedAt = at
}

func (s *Stats) Record(r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sent++
	if r.Category != CategorySuccess {
		s.failed++
	}

	method, ok := s.byMethod[r.Method]
	if !ok {
		method = &methodStats{}
		s.byMethod[r.Method] = method
	}

	method.sent++
	if r.Category != CategorySuccess {
		method.failed++
	}

	if r.ScheduledAt.Before(s.startedAt.Add(s.warmup)) {
		return
	}

	method.latencies = append(method.latencies, r.Latency())
}

func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := s.elapsed()

	snapshot := Snapshot{
		Elapsed: elapsed,
		Sent:    s.sent,
		Failed:  s.failed,
	}

	if elapsed > 0 {
		snapshot.RPS = float64(s.sent) / elapsed.Seconds()
	}

	all := make([]time.Duration, 0, s.sent)

	for name, method := range s.byMethod {
		all = append(all, method.latencies...)

		sorted := slices.Clone(method.latencies)
		slices.Sort(sorted)

		entry := MethodSnapshot{
			Method: name,
			Sent:   method.sent,
			Failed: method.failed,
			P50:    percentileSorted(sorted, 50),
			P90:    percentileSorted(sorted, 90),
			P99:    percentileSorted(sorted, 99),
		}
		if elapsed > 0 {
			entry.RPS = float64(method.sent) / elapsed.Seconds()
		}

		snapshot.Methods = append(snapshot.Methods, entry)
	}

	slices.SortFunc(snapshot.Methods, func(a, b MethodSnapshot) int {
		return strings.Compare(a.Method, b.Method)
	})

	slices.Sort(all)
	snapshot.P50 = percentileSorted(all, 50)
	snapshot.P90 = percentileSorted(all, 90)
	snapshot.P99 = percentileSorted(all, 99)

	return snapshot
}

func (s *Stats) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := s.elapsed()

	report := Report{
		Duration: elapsed,
		Sent:     s.sent,
		Failed:   s.failed,
	}

	for name, method := range s.byMethod {
		sorted := slices.Clone(method.latencies)
		slices.Sort(sorted)

		entry := MethodReport{
			Method:    name,
			Sent:      method.sent,
			Failed:    method.failed,
			Latencies: len(sorted),
			P50:       percentileSorted(sorted, 50),
			P90:       percentileSorted(sorted, 90),
			P95:       percentileSorted(sorted, 95),
			P99:       percentileSorted(sorted, 99),
		}

		if len(sorted) > 0 {
			entry.Min = sorted[0]
			entry.Max = sorted[len(sorted)-1]
		}
		if elapsed > 0 {
			entry.RPS = float64(method.sent) / elapsed.Seconds()
		}

		report.Methods = append(report.Methods, entry)
	}

	slices.SortFunc(report.Methods, func(a, b MethodReport) int {
		return strings.Compare(a.Method, b.Method)
	})

	return report
}

func (s *Stats) elapsed() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	if s.endedAt.IsZero() {
		return time.Since(s.startedAt)
	}

	return s.endedAt.Sub(s.startedAt)
}

func percentileSorted(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	index := (len(sorted)*p + 99) / 100
	if index > 0 {
		index--
	}

	return sorted[index]
}
