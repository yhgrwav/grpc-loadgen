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
	// Unanswered counts calls that never reached the target, so they are absent
	// from the distribution rather than recorded as very fast replies.
	Unanswered int
	// Windows holds one entry per second of the measured run, dense and indexed
	// by second (Windows[i].Second == i), so a consumer can see how load and its
	// latency decomposition moved over time. Empty for a run shorter than a
	// second or with no recorded calls.
	Windows []Window
}

// Window is one second of a method's run: how many calls were launched in it and
// the summed latency decomposition of the ones fully observed. Sums, not
// averages — the engine keeps the exact primitives and leaves any ratio to the
// consumer. Calls are placed by ScheduledAt, the second the load was meant for.
type Window struct {
	Second int
	// Sent counts every recorded call scheduled in this second, whatever its
	// outcome.
	Sent int
	// Decomposed counts the calls behind the sums below: those with a fully
	// observed latency. Unanswered calls (unreachable, unknown) and censored
	// ones (timeout, aborted) are counted in Sent but not here — their transport
	// and service times are unknown or only a lower bound, and adding them would
	// quietly understate the sums.
	Decomposed         int
	Queue              time.Duration
	Transport, Service time.Duration
}

type Report struct {
	Duration time.Duration
	Sent     int
	Failed   int
	Methods  []MethodReport
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
	byMethod  map[string]*methodStats
}

type methodStats struct {
	sent       int
	failed     int
	unanswered int
	latency    *metrics.Latencies
	// windows is dense and indexed by second since the measured run began; it
	// grows as the run advances and is never capped, so a long run keeps a
	// window per second.
	windows []windowCounters
}

// windowCounters is a window's live state, kept apart from the exported Window
// so recording touches ints and durations rather than building report structs.
type windowCounters struct {
	sent               int
	decomposed         int
	queue              time.Duration
	transport, service time.Duration
}

// window returns the counters for second idx, growing the slice to reach it. The
// pointer is used at once, under the same lock that guards every growth, so a
// later append relocating the backing array cannot strand it.
func (m *methodStats) window(idx int) *windowCounters {
	for len(m.windows) <= idx {
		m.windows = append(m.windows, windowCounters{})
	}

	return &m.windows[idx]
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

// Record files one finished call. Requests inside the warmup window are left
// out entirely — of the counters as much as of the distribution — because the
// report describes the measured part of the run, and counting them would skew
// the reported rate and hide the cold-start failures warmup exists to absorb.
func (s *Stats) Record(r Result) {
	s.mu.Lock()

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

	method, ok := s.byMethod[r.Method]
	if !ok {
		method = &methodStats{latency: metrics.NewLatencies()}
		s.byMethod[r.Method] = method
	}

	method.sent++
	if failed {
		method.failed++
	}

	// A call that never reached the target has no latency to record: a refused
	// connection comes back in microseconds and would pull both the median and
	// the tail down while the target is in fact unreachable.
	unanswered := r.Category == CategoryUnknown || r.Category == CategoryUnreachable
	if unanswered {
		method.unanswered++
	}

	if idx := s.windowIndex(r.ScheduledAt); idx >= 0 {
		w := method.window(idx)
		w.sent++

		// Only a fully observed latency joins the decomposition. A censored call
		// (timeout, aborted) knows its service time only as a lower bound, and an
		// unanswered one has none; either would understate the sums.
		if !unanswered && r.Category != CategoryTimeout && r.Category != CategoryAborted {
			w.decomposed++
			w.queue += r.QueueTime()
			w.transport += r.TransportWait()
			w.service += r.ServiceTime()
		}
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

	method.latency.Record(r.Latency())
}

// methodView is one method's counters taken under the lock together with a
// snapshot of its distribution, so percentiles can be computed without holding
// anything.
type methodView struct {
	name       string
	sent       int
	failed     int
	unanswered int
	dist       *metrics.Snapshot
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
	sources := make([]*metrics.Latencies, 0, len(s.byMethod))

	for name, method := range s.byMethod {
		out = append(out, methodView{
			name: name, sent: method.sent, failed: method.failed, unanswered: method.unanswered,
		})
		sources = append(sources, method.latency)
	}
	s.mu.Unlock()

	for i, src := range sources {
		out[i].dist = src.Snapshot()
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

func (s *Stats) Report() Report {
	elapsed, measured, sent, failed, views := s.views()

	s.mu.Lock()
	aborted := s.aborted
	s.mu.Unlock()

	windows := s.windowsByMethod()

	report := Report{
		Duration: elapsed,
		Sent:     sent,
		Failed:   failed,
		Aborted:  aborted,
	}

	for _, v := range views {
		entry := MethodReport{
			Method:     v.name,
			Sent:       v.sent,
			Failed:     v.failed,
			Latencies:  int(v.dist.Count()),
			Censored:   int(v.dist.CensoredCount()),
			Invalid:    int(v.dist.InvalidCount()),
			Unanswered: v.unanswered,
			Min:        v.dist.Percentile(0),
			P50:        v.dist.Percentile(0.50),
			P90:        v.dist.Percentile(0.90),
			P95:        v.dist.Percentile(0.95),
			P99:        v.dist.Percentile(0.99),
			Max:        v.dist.Percentile(1),
			Windows:    windows[v.name],
		}
		if measured > 0 {
			entry.RPS = float64(v.sent) / measured.Seconds()
		}

		report.Methods = append(report.Methods, entry)
	}

	return report
}

// windowIndex returns the second, counted from the first measured moment, that a
// call scheduled at scheduledAt belongs to, or -1 before the run has a start.
func (s *Stats) windowIndex(scheduledAt time.Time) int {
	if s.startedAt.IsZero() {
		return -1
	}

	since := scheduledAt.Sub(s.startedAt.Add(s.warmup))
	if since < 0 {
		return -1
	}

	return int(since / time.Second)
}

// windowsByMethod copies each method's windows into exported form under the lock.
// It is used only by Report at the end of the run, never on the Snapshot path,
// so the live view never pays to copy a run's worth of windows on every tick.
func (s *Stats) windowsByMethod() map[string][]Window {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string][]Window, len(s.byMethod))

	for name, method := range s.byMethod {
		if len(method.windows) == 0 {
			continue
		}

		windows := make([]Window, len(method.windows))
		for i := range method.windows {
			c := &method.windows[i]
			windows[i] = Window{
				Second: i, Sent: c.sent, Decomposed: c.decomposed,
				Queue: c.queue, Transport: c.transport, Service: c.service,
			}
		}

		out[name] = windows
	}

	return out
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
