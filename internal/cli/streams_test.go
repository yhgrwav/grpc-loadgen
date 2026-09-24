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
	"github.com/yhgrwav/leettest/pkg/metrics"
)

// oneStream is the end-to-end stream test's run as the report gets it: one
// connection, the target allows one stream, 48 of 50 calls waited ~130ms
// and timed out at 230ms, 100ms of which the target had them.
func oneStream() engine.Report {
	censoredAt := func(ms int) metrics.Quantile {
		return metrics.Quantile{Value: time.Duration(ms) * time.Millisecond, Defined: true}
	}

	return engine.Report{
		Duration: 5 * time.Second, Planned: 5 * time.Second, Sent: 50, Failed: 48,
		StreamWaited: 48, StreamWaitP99: exact(130), StreamCauseCalls: 48, StreamTailCalls: 48,
		Connections: &engine.Connections{Open: 1, LimitAnnounced: true, FirstLimit: 1, LastLimit: 1},
		Methods: []engine.MethodReport{{
			Method: "grpc.health.v1.Health/Check", Sent: 50, Failed: 48, TimedOut: 48,
			Latencies: 50, Censored: 48, Timeout: 230 * time.Millisecond, RPSLow: 10, RPSHigh: 10,
			P50: censoredAt(230), P90: censoredAt(230), P95: censoredAt(230), P99: censoredAt(230),
			P99WithoutClientWaits: censoredAt(100),
		}},
	}
}

func noteStarting(notes []string, prefix string) (string, bool) {
	for _, n := range notes {
		if strings.HasPrefix(n, prefix) {
			return n, true
		}
	}

	return "", false
}

// Ground: contract — the verdict's words carry the obligation: the run, not the target, hit
// the limit; the latency includes the wait; the target's capacity above limit × connections
// was not measured.
func TestNotes_AStreamLimitThatMovesTheP99IsAVerdict(t *testing.T) {
	const want = "limited by the run, not the target: on 1 connection the target allows 1 stream\n" +
		"(MAX_CONCURRENT_STREAMS), and 48 of 50 sent calls waited for one (p99 130ms).\n" +
		"The printed p99 includes the wait for 1 of 1 methods. The target was not tested\n" +
		"above 1 call in flight."

	got, ok := noteStarting(reportNotes(oneStream()), "limited by")
	if !ok {
		t.Fatalf("no stream verdict in:\n%s", strings.Join(reportNotes(oneStream()), "\n\n"))
	}
	if got != want {
		t.Errorf("verdict:\n%s\nwant:\n%s", got, want)
	}
}

// Ground: boundary — the verdict appears only when the wait changes a printed p99; at 3
// significant figures a small wait under a long tail changes nothing, and a verdict then would
// push the target's own verdict down for no printed reason.
func TestNotes_AWaitThatChangesNoPrintedNumberIsOnlyANote(t *testing.T) {
	report := oneStream()
	report.Sent, report.StreamWaited, report.StreamCauseCalls, report.StreamWaitP99 = 10000, 500, 500, metrics.Quantile{
		Value: 1200 * time.Microsecond, Exact: true, Defined: true,
	}
	m := &report.Methods[0]
	m.Sent, m.P99, m.P99WithoutClientWaits = 10000, exact(300), exact(300)

	notes := reportNotes(report)

	if v, ok := noteStarting(notes, "limited by"); ok {
		t.Errorf("a verdict though no printed number changes:\n%s", v)
	}
	const want = "500 of 10000 sent calls waited for a stream (p99 1.20ms); client-side waits (generator, connection, stream) did not move p99."
	if _, ok := noteStarting(notes, want); !ok {
		t.Errorf("no %q in:\n%s", want, strings.Join(notes, "\n\n"))
	}
}

// Ground: boundary — a same printed p99 with calls that never went out for want of a stream is
// still a verdict: those calls are missing from every percentile.
func TestNotes_UnsentForAStreamIsAVerdictEvenWithTheSameP99(t *testing.T) {
	report := oneStream()
	report.NotSent, report.NotSentStream = 2, 2
	report.StreamCauseCalls += 2
	report.StreamTailCalls += 2
	report.Methods[0].P99WithoutClientWaits = report.Methods[0].P99

	v, ok := noteStarting(reportNotes(report), "limited by")
	if !ok {
		t.Fatalf("no verdict with 2 calls unsent for want of a stream")
	}
	if !strings.Contains(v, "2 more were not sent") {
		t.Errorf("the verdict does not count the unsent calls:\n%s", v)
	}
}

// Ground: contract — with no limit announced the wait is not the target's doing, and the
// verdict must not name the target's limit.
func TestNotes_WaitingWithNoLimitAnnouncedDoesNotBlameTheTarget(t *testing.T) {
	report := oneStream()
	report.Connections.LimitAnnounced, report.Connections.FirstLimit, report.Connections.LastLimit = false, 0, 0

	v, ok := noteStarting(reportNotes(report), "limited by")
	if !ok {
		t.Fatalf("no verdict: the wait moved the p99")
	}
	if strings.Contains(v, "target allows") || !strings.Contains(v, "announced no limit") {
		t.Errorf("the verdict blames a limit the target never announced:\n%s", v)
	}
}

// Ground: contract — the limit a reconnecting target announces can change on each handshake;
// the line names the first and the last and how often, never the whole history, so it stays
// one line whatever the count.
func TestNotes_AChangingLimitIsOneBoundedLine(t *testing.T) {
	report := oneStream()
	report.Connections = &engine.Connections{
		Open: 1, Reconnects: 500, LimitAnnounced: true, FirstLimit: 1, LastLimit: 4, LimitChanges: 499,
	}

	n, ok := noteStarting(reportNotes(report), "connections: 1 (reconnects: 500)")
	if !ok {
		t.Fatalf("no connections line in:\n%s", strings.Join(reportNotes(report), "\n\n"))
	}
	if strings.Contains(n, "\n") || len(n) > 100 {
		t.Errorf("the line is not one bounded line: %q", n)
	}
	if !strings.Contains(n, "1 to 4, changed 499 times") {
		t.Errorf("the line does not give the first, the last and the count: %q", n)
	}
}

// Ground: contract — the three reasons are named, the empty ones left out, and they add up to
// the "not sent" of the summary line.
func TestNotes_NotSentIsSplitByReason(t *testing.T) {
	tests := []struct {
		name                     string
		late, stream, connection int
		want                     string
	}{
		{"all three", 1, 3, 1, "not sent 5: waited for a stream 3, waited for the connection 1, generator late 1."},
		{"only streams", 0, 3, 0, "not sent 3: waited for a stream 3."},
		{"only the connection", 0, 0, 2, "not sent 2: waited for the connection 2."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := oneStream()
			report.NotSentLate, report.NotSentStream, report.NotSentConnection = tt.late, tt.stream, tt.connection
			report.NotSent = tt.late + tt.stream + tt.connection

			if _, ok := noteStarting(reportNotes(report), tt.want); !ok {
				t.Errorf("no %q in:\n%s", tt.want, strings.Join(reportNotes(report), "\n\n"))
			}
		})
	}
}

// Ground: contract — the stream verdict explains the timeouts, so on the final screen it
// stands among the verdicts above the table, after "invalid run" and "incomplete", and the
// timeout note stays below.
func TestFinalScreen_TheStreamVerdictStandsAboveTheTimeoutNote(t *testing.T) {
	m := testModel(t)
	m.done, m.report = true, oneStream()
	m.report.Incomplete, m.report.Planned = true, 10*time.Second

	p := m.finalParts(120)

	var verdicts []string
	for _, v := range p.verdicts {
		verdicts = append(verdicts, ansiCodes.ReplaceAllString(strings.Join(v, " "), ""))
	}

	stream, incomplete := -1, -1
	for i, v := range verdicts {
		switch {
		case strings.Contains(v, "limited by the run"):
			stream = i
		case strings.Contains(v, "incomplete:"):
			incomplete = i
		}
	}

	if stream < 0 {
		t.Fatalf("the stream verdict is not among the verdicts: %q", verdicts)
	}
	if incomplete < 0 || stream < incomplete {
		t.Errorf("verdicts in order %q; want the stream one after incomplete", verdicts)
	}
}

// Ground: contract — a sender that says nothing about its connections leaves Connections nil;
// printing that as "connections: 0" or "no limit announced" would state something about the
// target nobody checked. Mutation "nil reads as the zero Connections" turns this red.
func TestNotes_NoConnectionDataNoConnectionLines(t *testing.T) {
	report := oneStream()
	report.Connections = nil

	for _, n := range reportNotes(report) {
		for _, claim := range []string{"connections:", "no limit", "announced", "target allows", "MAX_CONCURRENT_STREAMS"} {
			if strings.Contains(n, claim) {
				t.Errorf("with no connection data the report says %q:\n%s", claim, n)
			}
		}
	}
}

// Ground: contract — the verdict is about the run, the p99 without the wait is about a method;
// one number in the verdict would pass for the whole run. The verdict counts the methods whose
// printed p99 moves, and each of those gets its own note; a method whose p99 does not move gets
// none.
func TestNotes_TheVerdictCountsMethodsAndEachMovedOneGetsANote(t *testing.T) {
	report := oneStream()
	report.Methods[0].Method = "pkg.Svc/Put"
	report.Methods = append(report.Methods, engine.MethodReport{
		Method: "pkg.Svc/Get", Sent: 50, Latencies: 50, Timeout: time.Second,
		P50: exact(10), P90: exact(11), P95: exact(11), P99: exact(12), P99WithoutClientWaits: exact(12),
	})

	notes := reportNotes(report)

	v, ok := noteStarting(notes, "limited by")
	if !ok {
		t.Fatalf("no verdict in:\n%s", strings.Join(notes, "\n\n"))
	}
	if !strings.Contains(strings.ReplaceAll(v, "\n", " "), "includes the wait for 1 of 2 methods") {
		t.Errorf("the verdict does not count the methods the wait moved:\n%s", v)
	}
	if strings.Contains(v, "100ms") || strings.Contains(v, "12.0ms") {
		t.Errorf("the verdict names one method's p99 for the whole run:\n%s", v)
	}

	const put = "pkg.Svc/Put: p99 without client-side waits (generator, connection, stream) is >100ms."
	if _, ok := noteStarting(notes, put); !ok {
		t.Errorf("no %q in:\n%s", put, strings.Join(notes, "\n\n"))
	}
	if n, ok := noteStarting(notes, "pkg.Svc/Get: p99 without"); ok {
		t.Errorf("a note for a method the wait did not move: %q", n)
	}
}
