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
	"testing"
	"time"
)

func answered(start time.Time, category Category, latency time.Duration) Result {
	return Result{
		Method:      "a",
		ScheduledAt: start,
		BegunAt:     start,
		Outcome:     Outcome{SentAt: start, DoneAt: start.Add(latency), Category: category},
	}
}

// A target shedding load refuses 99% of calls in 2ms. Mixed in, p99 is about
// 2ms, and a `p99 < 200ms` threshold passes while almost nothing is served.
// Google's SRE book (Monitoring Distributed Systems, the four golden signals)
// says to tell the latency of successes from that of errors.
// Ground: contract — the refusal percentiles; FailuresOnAScheduleAreCountedExactly catches refusals
// mixed into service time (mutation 2026-09-22), not the refusal time itself.
func TestStats_RefusalsHaveTheirOwnLatency(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for range 990 {
		stats.Record(answered(start, CategoryOverload, 2*time.Millisecond))
	}
	for range 10 {
		stats.Record(answered(start, CategorySuccess, 200*time.Millisecond))
	}

	m := stats.Report().Methods[0]
	if got := m.P50; !got.Defined || got.Value < 199*time.Millisecond {
		t.Errorf("service p50 = %+v, want 200ms: the refusals are not service", got)
	}
	if m.Latencies != 10 {
		t.Errorf("service latencies = %d, want 10", m.Latencies)
	}
	if got := m.Refusal.P99; m.Refusal.Count != 990 || !got.Exact || got.Value > 3*time.Millisecond {
		t.Errorf("refusals = %d, p99 %+v; want 990 at 2ms", m.Refusal.Count, got)
	}
	if m.Failed != 990 {
		t.Errorf("failed = %d, want 990", m.Failed)
	}
}

// Ground: contract — server faults and client faults are refusals too, until a stand test refuses
// with every category; FailEvery uses one code.
func TestStats_EveryWayOfSayingNoIsARefusal(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for _, c := range []Category{CategoryServerFault, CategoryOverload, CategoryClientFault} {
		stats.Record(answered(start, c, time.Millisecond))
	}
	stats.Record(answered(start, CategorySuccess, 100*time.Millisecond))

	if m := stats.Report().Methods[0]; m.Refusal.Count != 3 || m.Latencies != 1 {
		t.Errorf("refusals %d, service latencies %d; want 3 and 1", m.Refusal.Count, m.Latencies)
	}
}

// Timeouts are the slowest calls of all: "not served within T" is a lower
// bound on the same service time. Dropped, the p99 of the survivors would hide
// the third that did not make it.
// Ground: contract — a timeout is a bound on service time and never a refusal.
func TestStats_TimeoutsStayInTheServiceTime(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for range 70 {
		stats.Record(answered(start, CategorySuccess, 900*time.Millisecond))
	}
	for range 30 {
		r := answered(start, CategoryTimeout, time.Second)
		r.Deadline = start.Add(time.Second)
		stats.Record(r)
	}

	m := stats.Report().Methods[0]
	if got := m.P99; got.Exact || got.Value < time.Second {
		t.Errorf("service p99 = %+v, want \"> 1s\", not the survivors' 900ms", got)
	}
	if m.Refusal.Count != 0 {
		t.Errorf("refusals = %d, want 0: a timeout is not a refusal", m.Refusal.Count)
	}
}

// Ground: contract — the live view shows service time; end-to-end tests read only the report.
func TestStats_LiveViewShowsServiceTime(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for range 99 {
		stats.Record(answered(start, CategoryOverload, 2*time.Millisecond))
	}
	stats.Record(answered(start, CategorySuccess, 200*time.Millisecond))

	if got := stats.Snapshot().P50; !got.Defined || got.Value < 199*time.Millisecond {
		t.Errorf("live p50 = %+v, want 200ms", got)
	}
}
