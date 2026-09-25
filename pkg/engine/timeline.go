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
// start. Begun is by the moment a call began, the outcomes by the moment it
// finished, the lag and the latency terms by the moment it was scheduled for.
type Second struct {
	Begun     int
	Succeeded int
	// TargetFailed is error statuses that came back: server faults and
	// overload, from the target or a proxy in front of it.
	TargetFailed int
	// TimedOut is calls that went out and got no answer within the timeout.
	TimedOut int
	// RequestFailed is client faults: the request itself was wrong, and the
	// target said so.
	RequestFailed int
	// UnsentLate and UnsentQuota are timeouts whose request never went out,
	// split by who ate more of the budget: the generator's lag, or the wait
	// on the connection from the start of the call to the deadline.
	UnsentLate  int
	UnsentQuota int
	// NotSentLate, NotSentStream and NotSentConnection split the unsent calls
	// by the rule of the report's totals.
	NotSentLate       int
	NotSentStream     int
	NotSentConnection int
	Unanswered        int
	// CutOff is calls that went out and got no status back.
	CutOff  int
	Aborted int
	// Unclassified is calls the sender left without a category: a defect of
	// the sender, not an observation about the target.
	Unclassified int
	// InFlight is how many calls had begun and not finished by the end of
	// this second. While the run goes on, the last timeout's worth of seconds
	// is not final yet: calls from them are still in flight and unrecorded.
	InFlight int
	// LagSum and LagMax are over every call begun, answered or not: LagCalls.
	LagSum   time.Duration
	LagMax   time.Duration
	LagCalls int
	// The observed sums are over successes only: ObservedCalls. They add up to
	// those calls' latencies. A refusal in 2ms would pass for a faster target,
	// and a timeout's service time is only a lower bound. So a drowning target
	// keeps a fine average here; its signal is TargetFailed.
	ObservedCalls    int
	ObservedLagSum   time.Duration
	TransportWaitSum time.Duration
	ServiceTimeSum   time.Duration
}

type second struct {
	begun, succeeded, targetFailed, timedOut, requestFailed int64
	// planned, answered and plannedTimedOut count calls by the second they
	// were scheduled for: how many, how many got a success or a status from
	// the target, how many went out and got nothing within their timeout.
	planned, answered, plannedTimedOut int64

	unsentLate, unsentQuota, unanswered, cutOff, aborted, unknown int64
	lagCalls, observedCalls                                       int64
	lagSum, lagMax, observedLag, transportWait, service           time.Duration
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

	p := &t.secs[scheduled]
	p.planned++
	switch r.Category {
	case CategorySuccess, CategoryServerFault, CategoryOverload, CategoryClientFault:
		p.answered++
	case CategoryTimeout:
		if !r.NotSent {
			p.plannedTimedOut++
		}
	}

	lag := r.QueueTime()
	if lag < 0 {
		t.invalidLag++
	} else {
		s := &t.secs[scheduled]
		s.lagCalls++
		s.lagSum += lag
		s.lagMax = max(s.lagMax, lag)

		if r.Category == CategorySuccess {
			s.observedCalls++
			s.observedLag += lag
			s.transportWait += r.TransportWait()
			s.service += r.ServiceTime()
		}
	}

	s := &t.secs[done]

	switch r.Category {
	case CategorySuccess:
		s.succeeded++
	case CategoryClientFault:
		s.requestFailed++
	case CategoryUnreachable:
		s.unanswered++
	case CategoryCutOff:
		s.cutOff++
	case CategoryAborted:
		s.aborted++
	case CategoryUnknown:
		s.unknown++
	case CategoryTimeout:
		switch {
		case !r.NotSent:
			s.timedOut++
		case lateMoreThanQueued(r):
			s.unsentLate++
		default:
			s.unsentQuota++
		}
	default:
		s.targetFailed++
	}
}

// lateMoreThanQueued reports whether the generator's lag ate more of an unsent
// call's budget than the wait on the connection did. A generator running
// behind starts calls just before their deadline, and a short quota wait
// then finishes them off; blaming the connection would advise more
// connections, which would not help.
func lateMoreThanQueued(r Result) bool {
	if r.Deadline.IsZero() {
		return false
	}

	return r.QueueTime() >= r.Deadline.Sub(r.BegunAt)
}

func (t *timeline) export() []Second {
	out := make([]Second, t.used)

	var inFlight int64

	for i := range t.secs[:t.used] {
		s := &t.secs[i]
		inFlight += s.begun - s.succeeded - s.targetFailed - s.timedOut - s.requestFailed - s.unsentLate -
			s.unsentQuota - s.unanswered - s.cutOff - s.aborted - s.unknown
		out[i] = Second{
			Begun:            int(s.begun),
			Succeeded:        int(s.succeeded),
			TargetFailed:     int(s.targetFailed),
			TimedOut:         int(s.timedOut),
			RequestFailed:    int(s.requestFailed),
			UnsentLate:       int(s.unsentLate),
			UnsentQuota:      int(s.unsentQuota),
			Unanswered:       int(s.unanswered),
			CutOff:           int(s.cutOff),
			Aborted:          int(s.aborted),
			Unclassified:     int(s.unknown),
			InFlight:         int(inFlight),
			LagSum:           s.lagSum,
			LagMax:           s.lagMax,
			LagCalls:         int(s.lagCalls),
			ObservedCalls:    int(s.observedCalls),
			ObservedLagSum:   s.observedLag,
			TransportWaitSum: s.transportWait,
			ServiceTimeSum:   s.service,
		}
	}

	return out
}

// silentFrom is the first scheduled second from which to the end no call got
// an answer while some went out and timed out. Seconds with nothing scheduled,
// or whose calls all ended on our side (cut off, never sent, unreachable), say
// nothing about the target and neither break nor start the stretch.
func (t *timeline) silentFrom() (int, bool) {
	from, found := 0, false

	for i := t.used - 1; i >= 0; i-- {
		s := &t.secs[i]
		switch {
		case s.answered > 0:
			return from, found
		case s.plannedTimedOut > 0:
			from, found = i, true
		}
	}

	return from, found
}
