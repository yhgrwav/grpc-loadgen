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

	"github.com/yhgrwav/grpc-loadgen/pkg/metrics"
)

type Snapshot struct {
	Elapsed  time.Duration
	Total    time.Duration
	Sent     int
	Failed   int
	InFlight int
	RPS      float64
	P50      metrics.Quantile
	P90      metrics.Quantile
	P99      metrics.Quantile
	Methods  []MethodSnapshot
}

type MethodSnapshot struct {
	Method    string
	Sent      int
	Failed    int
	RPS       float64
	TargetRPS int
	P50       metrics.Quantile
	P90       metrics.Quantile
	P99       metrics.Quantile
}

type MethodReport struct {
	Method string
	Sent   int
	Failed int
	RPS    float64
	Min    metrics.Quantile
	P50    metrics.Quantile
	P90    metrics.Quantile
	P95    metrics.Quantile
	P99    metrics.Quantile
	Max    metrics.Quantile
	// Latencies counts the observations behind the percentiles, Censored how
	// many of them only have a lower bound, and Invalid how many were rejected
	// as impossible — a negative latency means the time arithmetic is wrong.
	Latencies int
	Censored  int
	Invalid   int
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
	sent    int
	failed  int
	latency *metrics.Latencies
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

	s.sent++
	if r.Category != CategorySuccess {
		s.failed++
	}

	method, ok := s.byMethod[r.Method]
	if !ok {
		method = &methodStats{latency: metrics.NewLatencies()}
		s.byMethod[r.Method] = method
	}

	method.sent++
	if r.Category != CategorySuccess {
		method.failed++
	}

	warm := r.ScheduledAt.Before(s.startedAt.Add(s.warmup))
	s.mu.Unlock()

	if warm {
		return
	}

	// A call abandoned at its deadline lasted longer than the deadline by an
	// unknown amount, so its latency is a lower bound rather than a measurement.
	if r.Category == CategoryTimeout {
		method.latency.RecordCensored(r.Latency())
		return
	}

	method.latency.Record(r.Latency())
}

// methodView is one method's counters taken under the lock together with a
// snapshot of its distribution, so percentiles can be computed without holding
// anything.
type methodView struct {
	name   string
	sent   int
	failed int
	dist   *metrics.Snapshot
}

// views copies the counters under the lock and takes each distribution's
// snapshot outside it: every snapshot briefly locks its own distribution, and
// nesting those under the Stats lock would stall recording for as long as all
// methods together take to copy.
func (s *Stats) views() (elapsed time.Duration, sent, failed int, out []methodView) {
	s.mu.Lock()
	elapsed, sent, failed = s.elapsed(), s.sent, s.failed

	out = make([]methodView, 0, len(s.byMethod))
	sources := make([]*metrics.Latencies, 0, len(s.byMethod))

	for name, method := range s.byMethod {
		out = append(out, methodView{name: name, sent: method.sent, failed: method.failed})
		sources = append(sources, method.latency)
	}
	s.mu.Unlock()

	for i, src := range sources {
		out[i].dist = src.Snapshot()
	}

	slices.SortFunc(out, func(a, b methodView) int { return strings.Compare(a.name, b.name) })

	return elapsed, sent, failed, out
}

func (s *Stats) Snapshot() Snapshot {
	elapsed, sent, failed, views := s.views()

	snapshot := Snapshot{
		Elapsed: elapsed,
		Sent:    sent,
		Failed:  failed,
	}
	if elapsed > 0 {
		snapshot.RPS = float64(sent) / elapsed.Seconds()
	}

	dists := make([]*metrics.Snapshot, 0, len(views))

	for _, v := range views {
		dists = append(dists, v.dist)

		entry := MethodSnapshot{
			Method: v.name,
			Sent:   v.sent,
			Failed: v.failed,
			P50:    v.dist.Percentile(0.50),
			P90:    v.dist.Percentile(0.90),
			P99:    v.dist.Percentile(0.99),
		}
		if elapsed > 0 {
			entry.RPS = float64(v.sent) / elapsed.Seconds()
		}

		snapshot.Methods = append(snapshot.Methods, entry)
	}

	// Distributions are merged rather than their percentiles averaged: the mean
	// of two p99s is not the p99 of anything.
	overall := metrics.Merge(dists...)
	snapshot.P50 = overall.Percentile(0.50)
	snapshot.P90 = overall.Percentile(0.90)
	snapshot.P99 = overall.Percentile(0.99)

	return snapshot
}

func (s *Stats) Report() Report {
	elapsed, sent, failed, views := s.views()

	report := Report{
		Duration: elapsed,
		Sent:     sent,
		Failed:   failed,
	}

	for _, v := range views {
		entry := MethodReport{
			Method:    v.name,
			Sent:      v.sent,
			Failed:    v.failed,
			Latencies: int(v.dist.Count()),
			Censored:  int(v.dist.CensoredCount()),
			Invalid:   int(v.dist.InvalidCount()),
			Min:       v.dist.Percentile(0),
			P50:       v.dist.Percentile(0.50),
			P90:       v.dist.Percentile(0.90),
			P95:       v.dist.Percentile(0.95),
			P99:       v.dist.Percentile(0.99),
			Max:       v.dist.Percentile(1),
		}
		if elapsed > 0 {
			entry.RPS = float64(v.sent) / elapsed.Seconds()
		}

		report.Methods = append(report.Methods, entry)
	}

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
