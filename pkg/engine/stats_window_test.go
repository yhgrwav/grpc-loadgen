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
	"sync"
	"testing"
	"time"
)

// decomposedAt builds a fully observed call whose three latency terms are known
// by construction: it is scheduled at second `sec` of the measured run and its
// queue, transport and service times are exactly q, tr and sv.
func decomposedAt(method string, start time.Time, sec int, q, tr, sv time.Duration) Result {
	sched := start.Add(time.Duration(sec) * time.Second)
	begun := sched.Add(q)
	sent := begun.Add(tr)
	done := sent.Add(sv)

	return Result{
		Method:      method,
		ScheduledAt: sched,
		BegunAt:     begun,
		Outcome:     Outcome{SentAt: sent, DoneAt: done, Category: CategorySuccess},
	}
}

func methodWindows(t *testing.T, r Report, method string) []Window {
	t.Helper()

	for i := range r.Methods {
		if r.Methods[i].Method == method {
			return r.Methods[i].Windows
		}
	}

	t.Fatalf("method %q not in report", method)

	return nil
}

// A1, A11: a call lands in the second window of its ScheduledAt, and Report
// exposes the windows densely by second.
func TestRecord_AttributesToScheduledAtSecond(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(decomposedAt("m", start, 0, time.Millisecond, time.Millisecond, 5*time.Millisecond))
	stats.Record(decomposedAt("m", start, 2, time.Millisecond, time.Millisecond, 5*time.Millisecond))

	windows := methodWindows(t, stats.Report(), "m")
	if len(windows) != 3 {
		t.Fatalf("windows = %d, want 3 (seconds 0..2)", len(windows))
	}
	for i, w := range windows {
		if w.Second != i {
			t.Errorf("window %d carries Second %d", i, w.Second)
		}
	}
	if windows[0].Sent != 1 || windows[1].Sent != 0 || windows[2].Sent != 1 {
		t.Errorf("sent per second = %d,%d,%d, want 1,0,1",
			windows[0].Sent, windows[1].Sent, windows[2].Sent)
	}
}

// A1 (mutation guard): attribution is by ScheduledAt, not by DoneAt. A call
// scheduled in second 0 whose reply arrives five seconds later stays in
// window 0; nothing appears in window 5.
func TestRecord_AttributesByScheduledNotDone(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(decomposedAt("m", start, 0, 0, 0, 5*time.Second))

	windows := methodWindows(t, stats.Report(), "m")
	if len(windows) != 1 {
		t.Fatalf("windows = %d, want 1 (DoneAt in second 5 must not create windows)", len(windows))
	}
	if windows[0].Sent != 1 || windows[0].Service != 5*time.Second {
		t.Errorf("window 0 = {sent %d, service %s}, want {1, 5s}", windows[0].Sent, windows[0].Service)
	}
}

// A2: warmup calls fall in no window at all.
func TestRecord_WarmupCallsAreInNoWindow(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 2*time.Second)

	// Scheduled inside the warmup window: dropped entirely.
	stats.Record(decomposedAt("m", start, 0, 0, 0, time.Millisecond))
	stats.Record(decomposedAt("m", start, 1, 0, 0, time.Millisecond))
	// Scheduled exactly at the warmup boundary: the first measured second.
	stats.Record(decomposedAt("m", start, 2, 0, 0, time.Millisecond))

	report := stats.Report()
	if report.Sent != 1 {
		t.Fatalf("sent = %d, want 1 (warmup excluded)", report.Sent)
	}
	windows := methodWindows(t, report, "m")
	if len(windows) != 1 || windows[0].Sent != 1 {
		t.Fatalf("windows = %+v, want one window with sent 1", windows)
	}
}

// A3: sent counts every recorded call, whatever its category.
func TestRecord_SentCountsEveryCategory(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	sched := start
	for _, cat := range []Category{CategorySuccess, CategoryUnreachable, CategoryTimeout, CategoryAborted} {
		stats.Record(Result{
			Method:      "m",
			ScheduledAt: sched,
			BegunAt:     sched.Add(time.Millisecond),
			Deadline:    sched.Add(2 * time.Second),
			Outcome: Outcome{
				SentAt:   sched.Add(2 * time.Millisecond),
				DoneAt:   sched.Add(3 * time.Millisecond),
				Category: cat,
			},
		})
	}

	windows := methodWindows(t, stats.Report(), "m")
	if len(windows) != 1 || windows[0].Sent != 4 {
		t.Fatalf("window 0 sent = %+v, want sent 4", windows)
	}
	if windows[0].Decomposed != 1 {
		t.Errorf("decomposed = %d, want 1 (only the success)", windows[0].Decomposed)
	}
}

// A4, A5, A6, A7, A8, A9: only a fully observed latency enters the decomposition
// counters; unanswered and censored calls touch sent alone. The mandatory test
// for Unreachable (docs/decisions.md) and Aborted lives here.
func TestRecord_OnlyObservedLatencyEntersDecomposition(t *testing.T) {
	const (
		q  = 3 * time.Millisecond
		tr = 7 * time.Millisecond
		sv = 11 * time.Millisecond
	)

	for _, tt := range []struct {
		cat        Category
		decomposed bool
	}{
		{CategorySuccess, true},
		{CategoryClientFault, true},
		{CategoryServerFault, true},
		{CategoryOverload, true},
		{CategoryUnreachable, false}, // mandatory: refused connection is not in the decomposition
		{CategoryUnknown, false},
		{CategoryTimeout, false},
		{CategoryAborted, false}, // mandatory: aborted is censored, not in the decomposition
	} {
		t.Run(tt.cat.String(), func(t *testing.T) {
			stats := NewStats()
			start := time.Now()
			stats.Start(start, 0)

			r := decomposedAt("m", start, 0, q, tr, sv)
			r.Category = tt.cat
			r.Deadline = r.ScheduledAt.Add(2 * time.Second)
			stats.Record(r)

			w := methodWindows(t, stats.Report(), "m")[0]
			if w.Sent != 1 {
				t.Fatalf("sent = %d, want 1", w.Sent)
			}

			wantDecomposed, wantQ, wantTr, wantSv := 0, time.Duration(0), time.Duration(0), time.Duration(0)
			if tt.decomposed {
				wantDecomposed, wantQ, wantTr, wantSv = 1, q, tr, sv
			}
			if w.Decomposed != wantDecomposed {
				t.Errorf("decomposed = %d, want %d", w.Decomposed, wantDecomposed)
			}
			if w.Queue != wantQ || w.Transport != wantTr || w.Service != wantSv {
				t.Errorf("sums = {q %s, tr %s, sv %s}, want {%s, %s, %s}",
					w.Queue, w.Transport, w.Service, wantQ, wantTr, wantSv)
			}
		})
	}
}

// A9: sums are the exact arithmetic total over the calls that entered, so a
// second call adds to the first.
func TestRecord_DecompositionSumsAccumulate(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(decomposedAt("m", start, 0, 1*time.Millisecond, 2*time.Millisecond, 4*time.Millisecond))
	stats.Record(decomposedAt("m", start, 0, 2*time.Millisecond, 3*time.Millisecond, 5*time.Millisecond))

	w := methodWindows(t, stats.Report(), "m")[0]
	if w.Decomposed != 2 {
		t.Fatalf("decomposed = %d, want 2", w.Decomposed)
	}
	if w.Queue != 3*time.Millisecond || w.Transport != 5*time.Millisecond || w.Service != 9*time.Millisecond {
		t.Errorf("sums = {q %s, tr %s, sv %s}, want {3ms, 5ms, 9ms}", w.Queue, w.Transport, w.Service)
	}
}

// A10: windows are unbounded — a run longer than an hour keeps a window per
// second, with no cap at 60.
func TestRecord_WindowsAreUnbounded(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	stats.Record(decomposedAt("m", start, 3700, time.Millisecond, time.Millisecond, time.Millisecond))

	windows := methodWindows(t, stats.Report(), "m")
	if len(windows) != 3701 {
		t.Fatalf("windows = %d, want 3701 (no 60-window cap)", len(windows))
	}
	if windows[3700].Sent != 1 || windows[0].Sent != 0 {
		t.Errorf("expected the single call in second 3700, got sent[0]=%d sent[3700]=%d",
			windows[0].Sent, windows[3700].Sent)
	}
}

// A13: recording into an existing window makes no heap allocations.
func TestRecord_HotPathZeroAllocations(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	r := decomposedAt("m", start, 0, time.Millisecond, 2*time.Millisecond, 4*time.Millisecond)

	allocs := testing.AllocsPerRun(1000, func() {
		stats.Record(r)
	})
	if allocs != 0 {
		t.Errorf("Record allocated %v times per call, want 0 on the hot path", allocs)
	}
}

// A14: concurrent recording is race-free and loses no call.
func TestRecord_ConcurrentIsExact(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	const writers, each = 50, 200

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range each {
				stats.Record(decomposedAt("m", start, i%5, time.Millisecond, time.Millisecond, time.Millisecond))
			}
		}(w)
	}
	wg.Wait()

	total := 0
	for _, win := range methodWindows(t, stats.Report(), "m") {
		total += win.Sent
	}
	if total != writers*each {
		t.Errorf("sent across windows = %d, want %d", total, writers*each)
	}
}
