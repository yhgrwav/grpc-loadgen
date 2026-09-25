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

package measure

import (
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/test/stand"
)

// checkReasons asserts what every run must satisfy: the three reasons a call
// did not go out add up to the calls that did not.
func checkReasons(t *testing.T, report engine.Report) {
	t.Helper()

	if sum := report.NotSentGenerator + report.NotSentStream + report.NotSentConnection; sum != report.NotSent {
		t.Errorf("late %d + stream %d + connection %d = %d, not sent is %d",
			report.NotSentGenerator, report.NotSentStream, report.NotSentConnection, sum, report.NotSent)
	}
}

// The stand allows one stream and holds each call 150ms; calls come every
// 100ms with 230ms each. The stream is the bottleneck: it serves one call per
// 150ms while one arrives per 100ms, so a queue builds until each call gets the
// stream only when the one before it gives up at its deadline, 230ms after its
// own schedule. That is 130ms into the next call's budget, which then has
// 100ms of service left, short of 150: it times out too, and frees the stream
// for the one after. In that steady state every call but the first two
// waits ~130ms (the first waits 0, the second 50) and times out; the target
// answers only those two.
//
// The timeout is 230ms rather than 200, changed for the test's stability. At
// 200 two moments coincide and a race decides the outcome. Call 1, scheduled
// at 100ms, gets the stream at 150 and would be answered at 300, exactly its
// deadline. And each stream frees at the moment the next-but-one call is
// scheduled, so a newcomer can take it before the woken waiter does, and that
// waiter expires unsent: 1–2 of 30 in the prototype of 2026-09-23, at random.
// At 230 call 1 is answered 30ms before its deadline, and in steady state a
// call gets the stream 130ms into its budget with 100ms left, 30ms away from
// any arrival.
//
// Newcomers are not queued behind waiters: a call that finds quota free takes
// it without checking for anyone waiting (grpc-go v1.84.0
// internal/transport/http2_client.go:858-869, checkForStreamQuota on its first
// try), so a stray timing coincidence can still leave a waiter unsent, and
// the newcomer that took its stream is then served in full and answered.
//
// Measured 2026-09-23 under -race, 20 runs each on Windows and on Linux
// (golang:1.26 in Docker); 25 more plain runs on Windows before the wait
// existed. Not sent: 0 in all but one Windows run, which had 1. Timed out:
// 48 of 50, and 45 of 49 in that one run, where the newcomer got a free
// stream and an answer. Waited for a stream: 49 of 50 in every run (the
// first call waits 0). p99 of the wait: 130–131ms on both. p99 without the
// wait: 150–152ms, the two answered calls at the stand's 150ms.
// After stream waits began to count only with the open streams at the limit:
// 20 more runs under -race on Linux limited to 2 CPUs, like the CI runner,
// same: waited 49 of 50, p99 of the wait 131ms, not sent 0.
//
// Ranges: not sent at most 5 (10%, five times the worst seen); timed out at
// least 80% of the sent calls (worst seen 92%); waited at least 60% (seen
// 98%; well above 0, so "the wait is always 0" fails); p99 of the wait,
// over the calls that went out only, within 100–160ms (seen 130–131; below
// the 230ms timeout, so "the wait is the whole latency" fails). An unsent
// call waits to its deadline, ~230ms, and must not be in it.
//
// A call cut off by a stop while waiting for a stream is aborted, not unsent:
// Send returns the stop, the pool books it as CategoryAborted.
func TestReport_OneStreamIsWhereTheLatencyGoes(t *testing.T) {
	const (
		rps      = 10
		duration = 5 * time.Second
		timeout  = 230 * time.Millisecond
		hold     = 150 * time.Millisecond
	)

	target := stand.StartWith(stand.Constant(hold), grpc.MaxConcurrentStreams(1))
	t.Cleanup(target.Stop)

	report, method := run(t, target, load(target.Method(), rps, duration, timeout), budget(rps, timeout))
	checkReasons(t, report)

	conns := report.Connections
	if conns == nil || conns.Open != 1 || !conns.LimitAnnounced || conns.FirstLimit != 1 || conns.LastLimit != 1 {
		t.Errorf("connections %+v, want one connection with a limit of 1 announced by the target", conns)
	}

	if report.StreamWaited*10 < report.Sent*6 {
		t.Errorf("%d of %d sent calls waited for a stream, want at least 60%%", report.StreamWaited, report.Sent)
	}
	if p := report.StreamWaitP99; !p.Defined || p.Value < 100*time.Millisecond || p.Value > 160*time.Millisecond {
		t.Errorf("p99 of the stream wait %+v, want 100–160ms: the steady wait is 130", p)
	}
	if planned := rps * int(duration/time.Second); report.NotSent*10 > planned {
		t.Errorf("%d of %d calls not sent, want at most 10%%: a waiter gets the stream 100ms before its deadline",
			report.NotSent, planned)
	}
	if report.NotSentConnection != 0 {
		t.Errorf("%d calls put down to the connection, which was ready throughout", report.NotSentConnection)
	}

	if method.TimedOut*10 < method.Sent*8 {
		t.Errorf("%d of %d sent calls timed out, want at least 80%%: all but the first two in steady state",
			method.TimedOut, method.Sent)
	}
	if !method.P99WithoutClientWaits.Defined || method.P99WithoutClientWaits.Value >= method.P99.Value-50*time.Millisecond {
		t.Errorf("p99 %v, without the stream wait %+v: want the wait, ~130ms, out of it",
			method.P99.Value, method.P99WithoutClientWaits)
	}
}

// The sender counts a stream wait only with the open streams at the limit, and
// an unannounced limit is never reached: before that rule CI saw 1–3 of 600
// calls over 1ms here (run 35901535339), the scheduler's time, not a stream's.
//
// Ground: signal grpc-go v1.84.0 — a target that announces no limit leaves the client with
// none (http2_client.go:1344), so 300 calls held at once all reach it and none waits for a
// stream. The in-flight cap here is rps × timeout, 900, well above the 300 held at once: a
// lower cap would stop the run on our own ceiling and the test would check nothing.
func TestReport_NoAnnouncedLimitMeansNoStreamWait(t *testing.T) {
	const (
		rps      = 300
		duration = 2 * time.Second
		timeout  = 3 * time.Second
		hold     = time.Second
	)

	target := stand.Start(stand.Constant(hold))
	t.Cleanup(target.Stop)

	report, method := run(t, target, load(target.Method(), rps, duration, timeout), budget(rps, timeout))
	checkReasons(t, report)

	if conns := report.Connections; conns == nil || conns.LimitAnnounced {
		t.Fatalf("connections %+v, want one with no limit announced", conns)
	}
	if report.StreamWaited != 0 || report.NotSent != 0 {
		t.Errorf("%d calls waited for a stream, %d not sent; with no limit want none", report.StreamWaited, report.NotSent)
	}
	if method.TimedOut != 0 {
		t.Errorf("%d calls timed out; the stand answers each in %v of %v", method.TimedOut, hold, timeout)
	}
}

// The stand answers at once and cuts the connection at the 20th call, a
// second into the run, then holds the next dial for 500ms. Calls due in the first
// 300ms of that gap, ~6 at 20 rps, meet a connection that is not ready and
// expire after 200ms without going out; later ones outlive the gap and go
// out. The unsent ones waited for the connection, not a stream: no stream is
// ever busy here. Mutation "every unsent call is a stream wait"
// turns this red.
func TestReport_AReconnectIsAConnectionWaitNotAStreamWait(t *testing.T) {
	const (
		rps      = 20
		duration = 3 * time.Second
		timeout  = 200 * time.Millisecond
		gap      = 500 * time.Millisecond
	)

	target := stand.Start(stand.Constant(0))
	target.DropOnArrival(rps, gap)
	t.Cleanup(target.Stop)

	report, _ := run(t, target, load(target.Method(), rps, duration, timeout), budget(rps, timeout))
	checkReasons(t, report)

	if report.NotSentConnection == 0 {
		t.Errorf("no call put down to the connection; %d calls were due while it was down", int((gap-timeout)*rps/time.Second))
	}
	if report.NotSentStream != 0 {
		t.Errorf("%d calls put down to streams; no stream was ever busy", report.NotSentStream)
	}
	if conns := report.Connections; conns == nil || conns.Reconnects != 1 || conns.Open != 1 {
		t.Errorf("connections %+v, want one open and one reconnect", conns)
	}
}
