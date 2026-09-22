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

	"github.com/yhgrwav/leettest/pkg/metrics"
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
	// The percentiles above are the service time: successes, with timeouts and
	// aborted calls as lower bounds of it. Refusals — the target answering it
	// will not serve — are a different quantity, often far faster, and are in
	// Refusal instead.
	//
	// Latencies counts the observations behind the percentiles, Censored how
	// many of them only have a lower bound, and Invalid how many were rejected
	// as impossible — a negative latency means the time arithmetic is wrong.
	Latencies int
	Censored  int
	Invalid   int
	// Unanswered counts calls that never reached the target, so they are absent
	// from the distribution rather than recorded as very fast replies.
	Unanswered int
	// Unclassified counts calls the sender left without a category: a defect
	// of the sender, kept apart so it does not pass for an unreachable target.
	// They are absent from the distribution too.
	Unclassified int
	Refusal      RefusalLatency
	// Seconds covers the whole run, warmup included, up to the last second
	// anything happened in. Unlike the totals it keeps every call.
	Seconds []Second
	// OutsideTimeline counts calls left off Seconds because a moment of theirs
	// could not be placed on it; InvalidLag, calls begun before their schedule.
	OutsideTimeline int
	InvalidLag      int
}

// RefusalLatency is how long the target took to refuse: server faults,
// overload and client faults. A slow refusal is worse than a fast one.
type RefusalLatency struct {
	Count int
	P50   metrics.Quantile
	P90   metrics.Quantile
	P95   metrics.Quantile
	P99   metrics.Quantile
	Max   metrics.Quantile
}

type Report struct {
	Duration time.Duration
	// Warmup is the leading span of the run whose calls are on Seconds but not
	// in the totals: a call is warmup by the moment it was scheduled for.
	Warmup  time.Duration
	Sent    int
	Failed  int
	Methods []MethodReport
	// Aborted counts calls cut off by an abort of the run. They are no fault
	// of the target, so they are not in Failed; each is censored at the abort.
	Aborted int
	// Incomplete says the run ended before its plan, by Stop or by an abort.
	// Every number is honest, but it covers less than was asked for.
	Incomplete bool
}

type Stats struct {
	mu        sync.Mutex
	startedAt time.Time
	endedAt   time.Time
	warmup    time.Duration
	sent      int
	failed    int
	aborted   int
	reserve   int
	byMethod  map[string]*methodStats
}

type methodStats struct {
	sent       int
	failed     int
	unanswered int
	unknown    int
	latency    *metrics.Latencies
	refusal    *metrics.Latencies
	timeline   timeline
}

func NewStats() *Stats {
	return &Stats{byMethod: make(map[string]*methodStats)}
}

// Reserve fixes the span of the timeline: calls with a moment past it are
// counted in OutsideTimeline instead. The space for the named methods is
// taken here rather than on their first call, which records under the lock.
// Without Reserve the timeline is empty and every call is outside it.
func (s *Stats) Reserve(span time.Duration, methods ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reserve = int(span/time.Second) + 1
	for _, name := range methods {
		if _, ok := s.byMethod[name]; !ok {
			s.byMethod[name] = s.newMethod()
		}
	}
}

func (s *Stats) newMethod() *methodStats {
	return &methodStats{
		latency:  metrics.NewLatencies(),
		refusal:  metrics.NewLatencies(),
		timeline: newTimeline(s.reserve),
	}
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

// Record files one finished call. Requests inside the warmup window are left
// out of the counters as much as of the distribution — only the timeline keeps
// them — because the report describes the measured part of the run, and
// counting them would skew the reported rate and hide the cold-start failures
// warmup exists to absorb.
func (s *Stats) Record(r Result) {
	s.mu.Lock()

	method, ok := s.byMethod[r.Method]
	if !ok {
		method = s.newMethod()
		s.byMethod[r.Method] = method
	}

	// The timeline keeps warmup: a target failing on the way up is exactly
	// what it should show, and the report says which seconds were warmup.
	method.timeline.record(s.startedAt, r)

	if r.ScheduledAt.Before(s.startedAt.Add(s.warmup)) {
		s.mu.Unlock()

		return
	}

	s.sent++
	failed := r.Category != CategorySuccess && r.Category != CategoryAborted
	if failed {
		s.failed++
	}
	if r.Category == CategoryAborted {
		s.aborted++
	}

	method.sent++
	if failed {
		method.failed++
	}

	// A call that never reached the target has no latency to record: a refused
	// connection comes back in microseconds and would pull both the median and
	// the tail down while the target is in fact unreachable.
	unanswered := r.Category == CategoryUnknown || r.Category == CategoryUnreachable
	switch r.Category {
	case CategoryUnreachable:
		method.unanswered++
	case CategoryUnknown:
		method.unknown++
	}

	s.mu.Unlock()

	if unanswered {
		return
	}

	// An abandoned call is known only to have lasted at least as long as its
	// deadline, so it is recorded as a bound rather than as a measurement.
	if r.Category == CategoryTimeout || r.Category == CategoryAborted {
		method.latency.RecordCensored(r.CensorThreshold())
		return
	}

	if r.Category == CategorySuccess {
		method.latency.Record(r.Latency())
		return
	}

	method.refusal.Record(r.Latency())
}

// methodView is one method's counters taken under the lock together with a
// snapshot of its distribution, so percentiles can be computed without holding
// anything.
type methodView struct {
	name       string
	sent       int
	failed     int
	unanswered int
	unknown    int
	dist       *metrics.Snapshot
	refusal    *metrics.Snapshot
}

// views copies the counters under the lock and takes each distribution's
// snapshot outside it: every snapshot briefly locks its own distribution, and
// nesting those under the Stats lock would stall recording for as long as all
// methods together take to copy.
func (s *Stats) views() (elapsed, measured time.Duration, sent, failed int, out []methodView) {
	s.mu.Lock()
	elapsed, sent, failed = s.elapsed(), s.sent, s.failed

	// Rates divide by the measured window, not by the whole run: the counters
	// exclude warmup, so dividing by elapsed would report a rate lower than the
	// one actually driven.
	measured = elapsed - s.warmup
	if measured < 0 {
		measured = 0
	}

	out = make([]methodView, 0, len(s.byMethod))
	sources := make([]*methodStats, 0, len(s.byMethod))

	for name, method := range s.byMethod {
		out = append(out, methodView{
			name: name, sent: method.sent, failed: method.failed, unanswered: method.unanswered,
			unknown: method.unknown,
		})
		sources = append(sources, method)
	}
	s.mu.Unlock()

	for i, src := range sources {
		out[i].dist = src.latency.Snapshot()
		out[i].refusal = src.refusal.Snapshot()
	}

	slices.SortFunc(out, func(a, b methodView) int { return strings.Compare(a.name, b.name) })

	return elapsed, measured, sent, failed, out
}

func (s *Stats) Snapshot() Snapshot {
	elapsed, measured, sent, failed, views := s.views()

	snapshot := Snapshot{
		Elapsed: elapsed,
		Sent:    sent,
		Failed:  failed,
	}
	if measured > 0 {
		snapshot.RPS = float64(sent) / measured.Seconds()
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
		if measured > 0 {
			entry.RPS = float64(v.sent) / measured.Seconds()
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

// Report copies every method's timeline under the lock Record takes: call it
// once the run is over. For live data use Snapshot.
func (s *Stats) Report() Report {
	elapsed, measured, sent, failed, views := s.views()

	// The timelines are copied only here, once a run is over, never for the
	// snapshots the interface takes several times a second.
	s.mu.Lock()
	aborted, warmup := s.aborted, s.warmup
	timelines := make(map[string]MethodReport, len(s.byMethod))
	for name, method := range s.byMethod {
		timelines[name] = MethodReport{
			Seconds:         method.timeline.export(),
			OutsideTimeline: method.timeline.outside,
			InvalidLag:      method.timeline.invalidLag,
		}
	}
	s.mu.Unlock()

	report := Report{
		Duration: elapsed,
		Warmup:   warmup,
		Sent:     sent,
		Failed:   failed,
		Aborted:  aborted,
	}

	for _, v := range views {
		entry := MethodReport{
			Method:       v.name,
			Sent:         v.sent,
			Failed:       v.failed,
			Latencies:    int(v.dist.Count()),
			Censored:     int(v.dist.CensoredCount()),
			Invalid:      int(v.dist.InvalidCount()),
			Unanswered:   v.unanswered,
			Unclassified: v.unknown,
			Min:          v.dist.Percentile(0),
			P50:          v.dist.Percentile(0.50),
			P90:          v.dist.Percentile(0.90),
			P95:          v.dist.Percentile(0.95),
			P99:          v.dist.Percentile(0.99),
			Max:          v.dist.Percentile(1),
			Refusal: RefusalLatency{
				Count: int(v.refusal.Count()),
				P50:   v.refusal.Percentile(0.50),
				P90:   v.refusal.Percentile(0.90),
				P95:   v.refusal.Percentile(0.95),
				P99:   v.refusal.Percentile(0.99),
				Max:   v.refusal.Percentile(1),
			},

			Seconds:         timelines[v.name].Seconds,
			OutsideTimeline: timelines[v.name].OutsideTimeline,
			InvalidLag:      timelines[v.name].InvalidLag,
		}
		if measured > 0 {
			entry.RPS = float64(v.sent) / measured.Seconds()
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
