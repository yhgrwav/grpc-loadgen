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

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// waitCall is one successful call: how long it waited on each client-side
// cause, and how long the target had it after that.
type waitCall struct {
	lag, conn, stream, served time.Duration
}

// reportOf runs the calls through the engine's own statistics, so the
// verdict is tested on what the engine reports, not on a hand-made fixture.
func reportOf(t *testing.T, calls []waitCall) engine.Report {
	t.Helper()

	stats := engine.NewStats()
	start := time.Now()
	stats.Start(start, 0)

	sched := start.Add(10 * time.Millisecond)
	for _, c := range calls {
		begun := sched.Add(c.lag)
		sent := begun.Add(c.conn + c.stream)
		stats.Record(engine.Result{Method: "pkg.Svc/Do", ScheduledAt: sched, BegunAt: begun, Deadline: sched.Add(time.Second),
			Outcome: engine.Outcome{Category: engine.CategorySuccess, ConnWait: c.conn, StreamWait: c.stream,
				SentAt: sent, DoneAt: sent.Add(c.served)}})
	}
	stats.EndSending(start.Add(2 * time.Second))
	stats.Finish(start.Add(2 * time.Second))

	return stats.Report()
}

func times(n int, c waitCall) []waitCall {
	out := make([]waitCall, n)
	for i := range out {
		out[i] = c
	}

	return out
}

const ms = time.Millisecond

// The connection dropped and 200 calls waited 50ms each for it, then all went
// out: p99 is 52ms, of which the target had 2. Nothing was unsent, and the
// stream wait moved nothing, so before this the report said "p99 unchanged".
func TestVerdict_SentCallsThatWaitedForTheConnectionMoveP99(t *testing.T) {
	r := reportOf(t, times(200, waitCall{conn: 50 * ms, served: 2 * ms}))
	notes := strings.Join(reportNotes(r), "\n\n")

	v := verdictOf(t, r)
	if !strings.HasPrefix(v, "the connection to the target was not ready for 200 calls") {
		t.Errorf("heading:\n%s", v)
	}
	if strings.Contains(notes, "limited by the run") {
		t.Errorf("a connection not ready reads as the run's limit:\n%s", notes)
	}
	if !strings.Contains(notes, "pkg.Svc/Do: p99 without client-side waits (generator, connection, stream) is 2.00ms.") {
		t.Errorf("the note does not say what was taken out:\n%s", notes)
	}
}

// The generator fell 20ms behind on every call, and all went out.
func TestVerdict_SentCallsTheGeneratorStartedLateMoveP99(t *testing.T) {
	r := reportOf(t, times(200, waitCall{lag: 20 * ms, served: 2 * ms}))

	v := verdictOf(t, r)
	if !strings.HasPrefix(v, "limited by the run, not the target: the generator fell behind for 200 calls") {
		t.Errorf("heading:\n%s", v)
	}
}

// Three tail calls, each held 50ms by a different cause. Taking out any one
// cause leaves two at 52ms, and p99 of 100 calls stays 52ms; taking out all
// three brings it to 2ms. Only the one comparison sees it.
func TestVerdict_CausesThatMoveP99OnlyTogether(t *testing.T) {
	calls := times(97, waitCall{served: 2 * ms})
	calls = append(calls,
		waitCall{lag: 50 * ms, served: 2 * ms},
		waitCall{conn: 50 * ms, served: 2 * ms},
		waitCall{stream: 50 * ms, served: 2 * ms})
	r := reportOf(t, calls)

	v := verdictOf(t, r)
	if !strings.Contains(v, "causes, largest first: generator late 1; waited for a stream 1; connection not ready 1.") {
		t.Errorf("not every cause listed:\n%s", v)
	}
}

// Waits that moved no printed p99 are a note, and it says which waits it
// compared, not just that p99 did not change.
func TestNotes_ClientWaitsThatMovedNothingSayWhatWasCompared(t *testing.T) {
	// 2ms of 3s: at three significant figures p99 prints 3.00s either way.
	r := reportOf(t, times(100, waitCall{conn: 2 * ms, served: 3 * time.Second}))

	notes := strings.Join(reportNotes(r), "\n\n")
	if !strings.Contains(notes, "client-side waits (generator, connection, stream) did not move p99.") {
		t.Errorf("the note does not name what it compared:\n%s", notes)
	}
}
