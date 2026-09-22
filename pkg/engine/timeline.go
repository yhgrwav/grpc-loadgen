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

import "time"

// Second is one method's counters for one second of the run, counted from its
// start. Begun is by the moment a call began, the four outcomes by the moment
// it finished, the lag by the moment it was scheduled for.
type Second struct {
	Begun      int
	Succeeded  int
	Failed     int
	Unanswered int
	Aborted    int
	// InFlight is how many calls had begun and not finished by the end of
	// this second. While the run goes on, the last timeout's worth of seconds
	// is not final yet: calls from them are still in flight and unrecorded.
	InFlight int
	LagSum   time.Duration
	LagMax   time.Duration
}

type second struct {
	begun, succeeded, failed, unanswered, aborted int64
	lagSum, lagMax                                time.Duration
}

// timeline never grows while recording: growing means copying it under the
// lock every worker records behind, at the worst moment — a long drain or a
// lagging generator. Its span is fixed up front, and so is its memory.
type timeline struct {
	secs []second
	// used is how many leading seconds hold anything, so the reserve past the
	// last event is not reported as a run of empty seconds.
	used int
	// outside counts calls with a moment before the start or past the reserved
	// span, or that finished before they began. They are left off whole:
	// counting only one end would leave in flight wrong for good.
	outside    int
	invalidLag int
}

func newTimeline(span int) timeline {
	return timeline{secs: make([]second, span)}
}

func (t *timeline) secondOf(start, at time.Time) (int, bool) {
	d := at.Sub(start)
	if d < 0 {
		return 0, false
	}

	i := int(d / time.Second)

	return i, i < len(t.secs)
}

func (t *timeline) record(start time.Time, r Result) {
	scheduled, okScheduled := t.secondOf(start, r.ScheduledAt)
	begun, okBegun := t.secondOf(start, r.BegunAt)
	done, okDone := t.secondOf(start, r.DoneAt)

	if start.IsZero() || !okScheduled || !okBegun || !okDone || done < begun {
		t.outside++
		return
	}

	t.used = max(t.used, scheduled+1, begun+1, done+1)

	t.secs[begun].begun++

	if lag := r.QueueTime(); lag < 0 {
		t.invalidLag++
	} else {
		s := &t.secs[scheduled]
		s.lagSum += lag
		s.lagMax = max(s.lagMax, lag)
	}

	s := &t.secs[done]

	switch r.Category {
	case CategorySuccess:
		s.succeeded++
	case CategoryUnknown, CategoryUnreachable:
		s.unanswered++
	case CategoryAborted:
		s.aborted++
	default:
		s.failed++
	}
}

func (t *timeline) export() []Second {
	out := make([]Second, t.used)

	var inFlight int64

	for i, s := range t.secs[:t.used] {
		inFlight += s.begun - s.succeeded - s.failed - s.unanswered - s.aborted
		out[i] = Second{
			Begun:      int(s.begun),
			Succeeded:  int(s.succeeded),
			Failed:     int(s.failed),
			Unanswered: int(s.unanswered),
			Aborted:    int(s.aborted),
			InFlight:   int(inFlight),
			LagSum:     s.lagSum,
			LagMax:     s.lagMax,
		}
	}

	return out
}
