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

// busy is a Stats with ten methods, successes, refusals and timeouts on each.
func busy(t *testing.T) *Stats {
	t.Helper()

	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	for m := range 10 {
		method := fmt.Sprintf("svc.S/M%d", m)
		for i := range 200 {
			r := answered(start, CategorySuccess, time.Duration(i)*time.Millisecond)
			r.Method = method
			stats.Record(r)
		}
		r := answered(start, CategoryOverload, time.Millisecond)
		r.Method = method
		stats.Record(r)

		r = answered(start, CategoryTimeout, time.Second)
		r.Method, r.Deadline = method, start.Add(time.Second)
		stats.Record(r)
	}

	return stats
}

// Ground: public pkg/ API contract — the live view snapshots several times a
// second, and every allocation there is collector work in the generator's
// process: generator lag the tool causes itself.
func TestStats_RepeatedSnapshotIntoDoesNotAllocate(t *testing.T) {
	stats := busy(t)

	var dst Snapshot
	buf := NewLiveBuffer()

	for _, percentiles := range []bool{false, true} {
		stats.SnapshotInto(&dst, buf, percentiles)

		allocs := testing.AllocsPerRun(20, func() { stats.SnapshotInto(&dst, buf, percentiles) })
		if allocs != 0 {
			t.Errorf("percentiles %v: a repeated snapshot allocates %v times, want 0", percentiles, allocs)
		}
	}
}

// Ground: public pkg/ API contract — the live numbers are the report's
// numbers, taken through the allocating path.
func TestStats_SnapshotIntoMatchesTheReport(t *testing.T) {
	stats := busy(t)

	var got Snapshot
	stats.SnapshotInto(&got, NewLiveBuffer(), true)
	want := stats.Report()

	if got.Sent != want.Sent || got.Failed != want.Failed || len(got.Methods) != len(want.Methods) {
		t.Fatalf("sent %d failed %d methods %d; want %d, %d, %d",
			got.Sent, got.Failed, len(got.Methods), want.Sent, want.Failed, len(want.Methods))
	}

	for i, m := range got.Methods {
		w := want.Methods[i]
		if m.Method != w.Method || m.Sent != w.Sent || m.Failed != w.Failed ||
			m.P50 != w.P50 || m.P90 != w.P90 || m.P99 != w.P99 {
			t.Errorf("method %d = %+v, want %+v", i, m, w)
		}
	}
}

// Ground: public pkg/ API contract — between recomputations the caller keeps
// the percentiles it has, while the counters move on every call.
func TestStats_SnapshotWithoutPercentilesKeepsTheOnesItHas(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)
	stats.Record(answered(start, CategorySuccess, 100*time.Millisecond))

	var dst Snapshot
	buf := NewLiveBuffer()
	stats.SnapshotInto(&dst, buf, true)

	for range 99 {
		stats.Record(answered(start, CategorySuccess, 900*time.Millisecond))
	}
	stats.SnapshotInto(&dst, buf, false)

	if dst.Sent != 100 || dst.Methods[0].Sent != 100 {
		t.Errorf("sent %d, method sent %d; want 100 and 100", dst.Sent, dst.Methods[0].Sent)
	}
	if dst.P50.Value > 200*time.Millisecond || dst.Methods[0].P50.Value > 200*time.Millisecond {
		t.Errorf("p50 %v, method p50 %v; want the 100ms from the last recomputation",
			dst.P50.Value, dst.Methods[0].P50.Value)
	}
}

// Ground: public pkg/ API contract — what the engine adds on top, the target
// rates and calls in flight, must not allocate either.
func TestEngine_RepeatedSnapshotIntoDoesNotAllocate(t *testing.T) {
	eng, err := New(Options{
		Calls: []Call{
			{Method: "svc.S/A", Timeout: time.Second, Stages: []Stage{{StartRPS: 10, TargetRPS: 10, Duration: time.Second}}},
			{Method: "svc.S/B", Timeout: time.Second, Stages: []Stage{{StartRPS: 20, TargetRPS: 20, Duration: time.Second}}},
		},
		Sender:      FakeSender{},
		MaxInFlight: 100,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	start := time.Now()
	eng.stats.Start(start, 0)
	for _, method := range []string{"svc.S/A", "svc.S/B"} {
		r := answered(start, CategorySuccess, time.Millisecond)
		r.Method = method
		eng.stats.Record(r)
	}

	var dst Snapshot
	buf := NewLiveBuffer()
	eng.SnapshotInto(&dst, buf, true)

	if allocs := testing.AllocsPerRun(20, func() { eng.SnapshotInto(&dst, buf, true) }); allocs != 0 {
		t.Errorf("a repeated engine snapshot allocates %v times, want 0", allocs)
	}
	if dst.Methods[1].TargetRPS != 20 {
		t.Errorf("target rps of B = %d, want 20", dst.Methods[1].TargetRPS)
	}
}
