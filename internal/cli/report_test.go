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

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{in: 0, want: "0"},
		{in: 900 * time.Nanosecond, want: "900ns"},
		{in: 250 * time.Microsecond, want: "250us"},
		{in: 42 * time.Millisecond, want: "42ms"},
		{in: 3500 * time.Millisecond, want: "3.5s"},
		{in: 95 * time.Second, want: "1m35s"},
	}

	for _, tt := range tests {
		if got := formatDuration(tt.in); got != tt.want {
			t.Errorf("formatDuration(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPrintReport(t *testing.T) {
	report := engine.Report{
		Duration: 4 * time.Second,
		Sent:     1000,
		Failed:   22,
		Methods: []engine.MethodReport{
			{
				Method: "a.B/One", Sent: 800, Failed: 20, RPS: 200,
				P50: metrics.Quantile{Value: 31 * time.Millisecond, Exact: true, Defined: true},
				P99: metrics.Quantile{Value: 36 * time.Millisecond, Exact: true, Defined: true},
			},
		},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	text := out.String()

	for _, want := range []string{"localhost:50051", "sent 1000, failed 22", "a.B/One", "31ms", "36ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("report does not mention %q:\n%s", want, text)
		}
	}
}

func TestPrintReportMarksPercentilesThatRanPastTheTimeout(t *testing.T) {
	report := engine.Report{
		Duration: time.Second,
		Sent:     100,
		Methods: []engine.MethodReport{
			{
				Method: "a.B/One", Sent: 100, Censored: 3,
				P50: metrics.Quantile{Value: 10 * time.Millisecond, Exact: true, Defined: true},
				P99: metrics.Quantile{Value: time.Second, Defined: true},
			},
		},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	text := out.String()

	if !strings.Contains(text, ">1.0s") {
		t.Errorf("a percentile that ran past the deadline must be printed as a bound:\n%s", text)
	}
	if !strings.Contains(text, "3 requests were abandoned") {
		t.Errorf("the report must say how many requests were abandoned:\n%s", text)
	}
}

func TestPrintReportShowsUnmeasuredPercentileAsDash(t *testing.T) {
	report := engine.Report{
		Duration: time.Second,
		Methods:  []engine.MethodReport{{Method: "a.B/One"}},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	if !strings.Contains(out.String(), "-") {
		t.Errorf("a method with no measurements must not print a zero percentile:\n%s", out.String())
	}
}

func TestPrintReportShowsTheMethodAsTheConfigWritesIt(t *testing.T) {
	report := engine.Report{Methods: []engine.MethodReport{{Method: "/a.B/One", Sent: 1}}}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	if strings.Contains(out.String(), "/a.B/One") || !strings.Contains(out.String(), "a.B/One") {
		t.Errorf("report shows the gRPC path instead of the name from the config:\n%s", out.String())
	}
}

func TestPrintReportWarnsAboutCallsOffTheTimeline(t *testing.T) {
	report := engine.Report{
		Duration: time.Second,
		Sent:     10,
		Methods: []engine.MethodReport{
			{Method: "a.B/One", Sent: 7, OutsideTimeline: 2},
			{Method: "a.B/Two", Sent: 3, OutsideTimeline: 1},
		},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	if !strings.Contains(out.String(), "warning: 3 requests fell outside the per-second timeline") {
		t.Errorf("the report must say how many calls are missing from the timeline:\n%s", out.String())
	}

	out.Reset()
	report.Methods[0].OutsideTimeline, report.Methods[1].OutsideTimeline = 0, 0
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	if strings.Contains(out.String(), "timeline") {
		t.Errorf("no warning is due when nothing fell off the timeline:\n%s", out.String())
	}
}

// A call without a category is the sender's defect. Counted as unanswered it
// would read as a target that is down.
func TestPrintReportWarnsAboutUnclassifiedCalls(t *testing.T) {
	report := engine.Report{
		Duration: time.Second,
		Sent:     10,
		Methods: []engine.MethodReport{
			{Method: "a.B/One", Sent: 7, Unclassified: 2},
			{Method: "a.B/Two", Sent: 3, Unclassified: 1},
		},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	if !strings.Contains(out.String(), "warning: 3 requests came back without a category") {
		t.Errorf("the report must say how many calls the sender left unclassified:\n%s", out.String())
	}
	if strings.Contains(out.String(), "never reached the target") {
		t.Errorf("unclassified calls must not pass for an unreachable target:\n%s", out.String())
	}

	out.Reset()
	report.Methods = report.Methods[:0]
	PrintReport(&out, "localhost:50051", RunReport{Report: report})

	if strings.Contains(out.String(), "without a category") {
		t.Errorf("no unclassified calls, no warning:\n%s", out.String())
	}
}

// A target that refuses 99% of calls in 2ms must not show a 2ms p99: the
// method's row is the time to serve, and refusals get a row of their own.
func TestPrintReportTimesRefusalsOnTheirOwnRow(t *testing.T) {
	q := func(d time.Duration) metrics.Quantile { return metrics.Quantile{Value: d, Exact: true, Defined: true} }
	report := engine.Report{
		Duration: time.Second,
		Sent:     1000,
		Failed:   990,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 1000, Failed: 990,
			P50: q(200 * time.Millisecond), P99: q(201 * time.Millisecond),
			Refusal: engine.RefusalLatency{Count: 990, P50: q(2 * time.Millisecond), P99: q(3 * time.Millisecond)},
		}},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})
	text := out.String()

	var row string
	for line := range strings.Lines(text) {
		if strings.Contains(line, "refused") && strings.Contains(line, "990") {
			row = line
		}
	}
	if row == "" || !strings.Contains(row, "2ms") || !strings.Contains(row, "3ms") {
		t.Errorf("want a refused row with 990 calls, p50 2ms and p99 3ms:\n%s", text)
	}
	if !strings.Contains(text, "time to serve") {
		t.Errorf("the report must say the method's percentiles are the time to serve:\n%s", text)
	}

	out.Reset()
	report.Methods[0].Refusal = engine.RefusalLatency{}
	PrintReport(&out, "localhost:50051", RunReport{Report: report})
	if strings.Contains(out.String(), "refused") {
		t.Errorf("no refusals, no refused row:\n%s", out.String())
	}
}

func TestPrintReportSaysACapHitIsTheGenerators(t *testing.T) {
	report := engine.Report{
		Duration: 1200 * time.Millisecond, Planned: 2 * time.Second, Incomplete: true,
		CapHit:  &engine.CapHit{At: 1200 * time.Millisecond, Unsent: 1, OverDeadline: 21},
		Methods: []engine.MethodReport{{Method: "a.B/One", Sent: 1200}},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})
	text := out.String()

	// The verdict names the allowance and both causes, and gives no delay:
	// at the moment of the cap it is always about the allowance.
	for _, want := range []string{"invalid", "more than 100ms", "CPU", "sender", "deadline", "21", "ran 1.2s of the planned 2.0s"} {
		if !strings.Contains(text, want) {
			t.Errorf("report does not mention %q:\n%s", want, text)
		}
	}

	// The count and its caption must say the same thing: 21 slots were held
	// past their deadline, only some of them past the allowance. A caption
	// reading "21 calls broke the allowance" would be false.
	if !strings.Contains(text, "21 slots") || !strings.Contains(text, "past their own deadline") {
		t.Errorf("the count is not captioned for what it counts:\n%s", text)
	}

	// Nothing is said about how far past the deadline they were: that is not
	// counted. With several methods in one budget the cap can fall seconds
	// after the first deadline, and "most of them within the allowance" would
	// be false.
	for _, wrong := range []string{"most of them", "allowance;", "within the allowance"} {
		if strings.Contains(text, wrong) {
			t.Errorf("the caption claims %q, which nothing measures:\n%s", wrong, text)
		}
	}
}

func TestPrintReportStatesWhatTheTargetDidNotAnswer(t *testing.T) {
	zero := 0
	report := engine.Report{
		Duration: 3 * time.Second,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 150, Failed: 150, TimedOut: 150, UnsentTimedOut: 4,
			SilentFrom: &zero, RPSLow: 50, RPSHigh: 50, Timeout: 300 * time.Millisecond,
		}},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})
	text := out.String()

	for _, want := range []string{"at 50 rps", "150 of 150", "100.0%", "no answer within 300ms",
		"answered nothing at all", "4"} {
		if !strings.Contains(text, want) {
			t.Errorf("report does not mention %q:\n%s", want, text)
		}
	}
	for _, wrong := range []string{"does not hold", "cannot handle"} {
		if strings.Contains(text, wrong) {
			t.Errorf("report says %q: 0a states what was measured, with no threshold:\n%s", wrong, text)
		}
	}
}

func TestPrintReportStatesARateRangeAndNoSilenceClauseWithoutOne(t *testing.T) {
	report := engine.Report{
		Duration: 3 * time.Second,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 150, TimedOut: 50, RPSLow: 20, RPSHigh: 40, Timeout: 300 * time.Millisecond,
		}},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})
	text := out.String()

	if !strings.Contains(text, "at 20-40 rps") || !strings.Contains(text, "50 of 150") {
		t.Errorf("report lacks the range and the count:\n%s", text)
	}
	if strings.Contains(text, "nothing after") || strings.Contains(text, "answered nothing at all") {
		t.Errorf("report names a silent second, yet the target answered to the end:\n%s", text)
	}
}

// Both lines are about one method: a blank line between them would read as two
// separate findings, and the screen, which wraps each note on its own, would
// split them too.
func TestPrintReportKeepsAMethodsUnsentTimeoutsWithItsSilence(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: 3 * time.Second,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 150, TimedOut: 146, UnsentTimedOut: 4,
			RPSLow: 50, RPSHigh: 50, Timeout: 300 * time.Millisecond,
		}},
	}})

	text := out.String()
	if !strings.Contains(text, "within 300ms.\na.B/One: 4 calls timed out before going out") {
		t.Errorf("the method's two lines are not one block:\n%s", text)
	}
}

func TestPrintReportNamesStartLagForWhatItMeasures(t *testing.T) {
	report := engine.Report{
		Duration:    time.Second,
		StartLagP99: metrics.Quantile{Value: 3 * time.Millisecond, Exact: true, Defined: true},
		StartLagMax: 12 * time.Millisecond, LateCancelMax: 7 * time.Millisecond,
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: report})
	text := out.String()

	for _, want := range []string{"start lag", "3ms", "12ms", "7ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("report does not mention %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "generator lag") {
		t.Errorf("start lag is not the generator's lag as a whole: it does not see answers picked up late:\n%s", text)
	}
}

// The column carries what the generator sent per second of sending, not the
// target's throughput and not an average over the drain: a header reading
// "rps" leaves the reader to guess which.
func TestPrintReportNamesTheRateColumnForWhatItCounts(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: time.Second,
		Methods:  []engine.MethodReport{{Method: "a.B/One", Sent: 100, RPS: 100}},
	}})

	if text := out.String(); !strings.Contains(text, "sent/s") {
		t.Errorf("rate column is not named sent/s:\n%s", text)
	}
}

// A target that answers "no such method" says nothing about load: counted with
// overload refusals it would read as a service shedding requests.
func TestPrintReportSeparatesARejectedRequestFromARefusal(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration:        time.Second,
		Sent:            100,
		Failed:          100,
		RequestRejected: true,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 100, Failed: 100, RPS: 100,
			Rejected: engine.RefusalLatency{
				Count: 100,
				P50:   metrics.Quantile{Value: 300 * time.Microsecond, Exact: true, Defined: true},
			},
		}},
	}})

	text := out.String()
	if !strings.Contains(text, "rejected") {
		t.Errorf("no rejected row:\n%s", text)
	}
	for _, want := range []string{"invalid run", "a.B/One", "client's 4MB limit", "larger than it accepts"} {
		if !strings.Contains(text, want) {
			t.Errorf("verdict does not say %q:\n%s", want, text)
		}

		// The two causes are told apart: a reply the client cut off, and a request
		// the target refused. The target's own limit is unknown to us, so no number
		// is put on it.
		if strings.Contains(text, "cut off") || strings.Contains(text, "truncat") {
			t.Errorf("the report suggests a partial reply arrived; grpc-go rejects it whole:\n%s", text)
		}
		if strings.Contains(text, "target's 4MB") || strings.Contains(text, "4MB limit or a request") {
			t.Errorf("the report puts our client limit on the target:\n%s", text)
		}
	}
}

// With three methods and one rejected outright, the verdict is about that
// method: a share taken over the run would be 33% and no verdict at all.
func TestPrintReportNamesTheMethodWhoseRequestsAreRejected(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration:        time.Second,
		Sent:            300,
		Failed:          100,
		RequestRejected: true,
		Methods: []engine.MethodReport{
			{Method: "a.B/Good", Sent: 100, RPS: 100},
			// Some of its calls were rejected, not all: the target does serve this
			// method, so the verdict is not about it.
			{Method: "a.B/Partly", Sent: 100, Failed: 10, RPS: 100,
				Rejected: engine.RefusalLatency{Count: 10}},
			{Method: "a.B/Typo", Sent: 100, Failed: 100, RPS: 100,
				Rejected: engine.RefusalLatency{Count: 100}},
		},
	}})

	text := out.String()
	at := strings.Index(text, "invalid run")
	if at < 0 {
		t.Fatalf("no verdict at all:\n%s", text)
	}
	verdict := text[at:]

	if !strings.Contains(verdict, "a.B/Typo") {
		t.Errorf("the verdict does not name the method it is about:\n%s", verdict)
	}
	for _, served := range []string{"a.B/Good", "a.B/Partly"} {
		if strings.Contains(verdict, served) {
			t.Errorf("the verdict names %s, which the target does serve:\n%s", served, verdict)
		}
	}
}

// A bucket number moves the start of the silence by up to a second and reads
// as a clock time anyway: the moment the last answered call was scheduled for
// does not.
func TestPrintReportNamesTheMomentTheAnswersStopped(t *testing.T) {
	last := 8 * time.Second
	from := 8

	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: 15 * time.Second,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 7500, Failed: 3499, TimedOut: 3499,
			SilentFrom: &from, LastAnswerAt: &last, RPSLow: 500, RPSHigh: 500,
			Timeout: 2 * time.Second,
		}},
	}})

	text := out.String()
	if !strings.Contains(text, "8.0s") {
		t.Errorf("report does not name the moment of the last answer:\n%s", text)
	}
	if strings.Contains(text, "second 8") || strings.Contains(text, "second 9") {
		t.Errorf("report still counts buckets:\n%s", text)
	}
}
