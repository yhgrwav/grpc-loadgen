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
	"context"
	"testing"
	"time"
)

// recordUnsent records one call that timed out before going out: begun lag after
// its schedule, with a budget of timeout from the schedule.
func recordUnsent(stats *Stats, at time.Time, lag, timeout time.Duration, on Blocker) {
	stats.Record(Result{
		Method: "a", ScheduledAt: at, BegunAt: at.Add(lag), Deadline: at.Add(timeout),
		Outcome: Outcome{
			Category: CategoryTimeout, NotSent: true, NotSentOn: on,
			SentAt: at.Add(timeout), DoneAt: at.Add(timeout),
		},
	})
}

// Ground: boundary — which reason an unsent call goes to is decided by a comparison of two
// durations; an end-to-end test cannot put a call exactly on it without conducting time to the
// millisecond.
func TestStats_NotSentSplitsIntoThreeReasonsThatAddUp(t *testing.T) {
	const timeout = 200 * time.Millisecond

	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start.Add(time.Second)
	// Late: the generator ate 150 of 200ms, the connection 50.
	recordUnsent(stats, at, 150*time.Millisecond, timeout, BlockedOnStream)
	// Late on the boundary: 100 and 100 goes to the generator (>=).
	recordUnsent(stats, at, 100*time.Millisecond, timeout, BlockedOnStream)
	// Stream: 10ms late, 190 waiting on a ready connection with no stream.
	recordUnsent(stats, at, 10*time.Millisecond, timeout, BlockedOnStream)
	recordUnsent(stats, at, 0, timeout, BlockedOnStream)
	// Connection: the connection was not ready for some of the wait.
	recordUnsent(stats, at, 10*time.Millisecond, timeout, BlockedOnConnection)

	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	r := stats.Report()

	if r.NotSent != 5 {
		t.Fatalf("not sent %d, want 5", r.NotSent)
	}
	if r.NotSentLate != 2 || r.NotSentStream != 2 || r.NotSentConnection != 1 {
		t.Errorf("late %d, stream %d, connection %d; want 2, 2, 1", r.NotSentLate, r.NotSentStream, r.NotSentConnection)
	}
	if sum := r.NotSentLate + r.NotSentStream + r.NotSentConnection; sum != r.NotSent {
		t.Errorf("reasons add up to %d, not sent is %d", sum, r.NotSent)
	}
}

// Ground: contract — zero calls must give zero reasons, not a sum that differs from NotSent.
func TestStats_NoCallsNoReasons(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)
	stats.EndSending(start)
	stats.Finish(start)

	r := stats.Report()

	if r.NotSentLate != 0 || r.NotSentStream != 0 || r.NotSentConnection != 0 || r.StreamWaited != 0 {
		t.Errorf("late %d, stream %d, connection %d, waited %d; want all zero",
			r.NotSentLate, r.NotSentStream, r.NotSentConnection, r.StreamWaited)
	}
	if r.StreamWaitP99.Defined {
		t.Errorf("stream wait p99 %v is defined over no calls", r.StreamWaitP99.Value)
	}
}

// sentAfter records a success that waited wait for a stream and lasted
// latency from its schedule.
func sentAfter(stats *Stats, at time.Time, wait, latency time.Duration) {
	stats.Record(Result{
		Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(time.Second),
		Outcome: Outcome{
			Category: CategorySuccess, StreamWait: wait,
			SentAt: at.Add(wait), DoneAt: at.Add(latency),
		},
	})
}

// Ground: boundary — the floor that decides "waited" is a millisecond, and a wait exactly on
// it must not count; an end-to-end test cannot set a wait to the microsecond.
func TestStats_StreamWaitIsOverSentCallsAboveTheFloor(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start.Add(time.Second)
	sentAfter(stats, at, 0, 10*time.Millisecond)
	sentAfter(stats, at, StreamWaitFloor, 10*time.Millisecond)
	sentAfter(stats, at, StreamWaitFloor+time.Microsecond, 10*time.Millisecond)
	sentAfter(stats, at, 80*time.Millisecond, 100*time.Millisecond)
	// Unsent calls waited to their deadline, and they are counted as unsent, not
	// here: in the p99 they would pull it to the timeout.
	for range 5 {
		stats.Record(Result{
			Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(200 * time.Millisecond),
			Outcome: Outcome{
				Category: CategoryTimeout, NotSent: true, NotSentOn: BlockedOnStream,
				StreamWait: 190 * time.Millisecond, SentAt: at.Add(200 * time.Millisecond), DoneAt: at.Add(200 * time.Millisecond),
			},
		})
	}

	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	r := stats.Report()

	if r.StreamWaited != 2 {
		t.Errorf("waited %d, want 2: a wait at the floor is not a wait, and unsent calls are not sent ones", r.StreamWaited)
	}
	// The top of 80ms's HDR bucket at 3 significant digits (hdrhistogram-go v1.3.0).
	if want := 80019455 * time.Nanosecond; !r.StreamWaitP99.Defined || r.StreamWaitP99.Value != want {
		t.Errorf("stream wait p99 %+v, want %v: 80ms, over the sent calls that waited", r.StreamWaitP99, want)
	}
}

// Ground: contract — the p99 the target saw is the same calls with the wait taken out; the
// verdict compares the two, so a wrong one shows a limit where there is none or hides one.
func TestStats_P99WithoutStreamWaitTakesTheWaitOut(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start.Add(time.Second)
	// 100 calls, each 300ms long, of which 200 waiting for a stream.
	for range 100 {
		sentAfter(stats, at, 200*time.Millisecond, 300*time.Millisecond)
	}

	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	m := stats.Report().Methods[0]

	if !m.P99.Defined || m.P99.Value < 299*time.Millisecond {
		t.Fatalf("p99 %+v, want ~300ms", m.P99)
	}
	// The top of 100ms's HDR bucket.
	if want := 100007935 * time.Nanosecond; !m.P99WithoutStreamWait.Defined || m.P99WithoutStreamWait.Value != want {
		t.Errorf("p99 without stream wait %+v, want %v: 100ms", m.P99WithoutStreamWait, want)
	}
}

// Ground: contract — a timeout's p99 is censored at the timeout; taking the wait out of a
// lower bound leaves a lower bound, and it must stay censored rather than pass for a measurement.
func TestStats_P99WithoutStreamWaitOfTimeoutsStaysCensored(t *testing.T) {
	stats := NewStats()
	start := time.Now()
	stats.Start(start, 0)

	at := start.Add(time.Second)
	for range 100 {
		stats.Record(Result{
			Method: "a", ScheduledAt: at, BegunAt: at, Deadline: at.Add(200 * time.Millisecond),
			Outcome: Outcome{
				Category: CategoryTimeout, StreamWait: 100 * time.Millisecond,
				SentAt: at.Add(100 * time.Millisecond), DoneAt: at.Add(200 * time.Millisecond),
			},
		})
	}

	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	m := stats.Report().Methods[0]

	if !m.P99.Defined || m.P99.Exact {
		t.Fatalf("p99 %+v, want censored at the timeout", m.P99)
	}
	if !m.P99WithoutStreamWait.Defined || m.P99WithoutStreamWait.Exact {
		t.Errorf("p99 without stream wait %+v, want censored: the target never answered", m.P99WithoutStreamWait)
	}
}

type reportingSender struct {
	FakeSender
	conns Connections
}

func (s reportingSender) Connections() (Connections, bool) { return s.conns, true }

type unknownSender struct{ FakeSender }

func (unknownSender) Connections() (Connections, bool) { return Connections{}, false }

// Ground: contract — the engine hands on what the sender knows about its connections and
// leaves the field nil for a sender that does not tell.
func TestEngine_ReportCarriesTheSendersConnections(t *testing.T) {
	want := Connections{Open: 1, Reconnects: 2, LimitAnnounced: true, FirstLimit: 1, LastLimit: 4, LimitChanges: 1}

	for _, tt := range []struct {
		name   string
		sender Sender
		want   *Connections
	}{
		{"reporting", reportingSender{conns: want}, &want},
		{"silent", FakeSender{}, nil},
		// Reporting, but with nothing it saw: a zero Connections would read as a
		// target that announced no limit.
		{"unknown", unknownSender{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			eng, err := New(Options{
				Calls:       []Call{{Method: "a", Timeout: time.Second, Stages: []Stage{{StartRPS: 10, TargetRPS: 10, Duration: 100 * time.Millisecond}}}},
				Sender:      tt.sender,
				MaxInFlight: 20,
			})
			if err != nil {
				t.Fatalf("build the engine: %v", err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			if err := eng.Run(ctx); err != nil {
				t.Fatalf("run: %v", err)
			}

			got := eng.Report().Connections
			switch {
			case (got == nil) != (tt.want == nil):
				t.Fatalf("connections %v, want %v", got, tt.want)
			case got != nil && *got != *tt.want:
				t.Errorf("connections %+v, want %+v", *got, *tt.want)
			}
		})
	}
}
