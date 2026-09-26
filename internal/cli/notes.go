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
	"fmt"
	"strings"
	"time"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// reportNotes is everything the report says in words: what the target did,
// what the generator did, and the verdicts. One source for both the text
// report and the final screen of the live view, so what the screen shows and
// what the log keeps cannot drift apart.
func reportNotes(report engine.Report, maxResponse string) []string {
	notes := make([]string, 0, 8)
	add := func(format string, args ...any) {
		notes = append(notes, strings.TrimRight(fmt.Sprintf(format, args...), "\n"))
	}

	// Sent leaves the warmup out; this line holds it, so the two add up to
	// every call that went out.
	if report.Warmup > 0 {
		failed := ""
		if report.WarmupFailed > 0 {
			failed = fmt.Sprintf(" (%d failed)", report.WarmupFailed)
		}
		add("warm-up %d sent%s, excluded from stats", report.WarmupSent, failed)
	}

	censored, unanswered, cutOff, unclassified, outside, invalid := 0, 0, 0, 0, 0, 0
	rejected := make([]string, 0, len(report.Methods))
	for i := range report.Methods {
		m := &report.Methods[i]
		censored += m.Censored
		invalid += m.Invalid
		unanswered += m.Unanswered
		cutOff += m.CutOff
		unclassified += m.Unclassified
		outside += m.OutsideTimeline
		if m.Rejected.Count > 0 {
			rejected = append(rejected, displayMethod(m.Method))
		}
	}

	// What the target did: stated as measured, with no threshold. Whether the
	// rate is more than it can take needs one, and picking it is not this
	// report's job.
	for i := range report.Methods {
		m := &report.Methods[i]

		rate := fmt.Sprintf("%d", m.RPSLow)
		if m.RPSHigh != m.RPSLow {
			rate = fmt.Sprintf("%d-%d", m.RPSLow, m.RPSHigh)
		}

		// Both facts are about one method, so they make one note.
		var lines []string
		if m.TimedOut > 0 {
			silence := ""
			switch {
			case m.SilentFrom != nil && m.LastAnswerAt != nil:
				silence = fmt.Sprintf(",\nand nothing after the call scheduled at %s of the run got one",
					formatDuration(*m.LastAnswerAt))
			case m.SilentFrom != nil:
				silence = ",\nand the target answered nothing at all"
			}

			lines = append(lines, fmt.Sprintf("%s: at %s rps, %d of %d calls (%.1f%%) got no answer within %s%s.",
				displayMethod(m.Method), rate, m.TimedOut, m.Sent, share(m.TimedOut, m.Sent),
				formatDuration(m.Timeout), silence))
			if m.TimedOutAfterWait > 0 {
				lines = append(lines, fmt.Sprintf("%d of them went out with less than half the timeout left: the target had\n"+
					"the smaller part of it.", m.TimedOutAfterWait))
			}
		}

		if m.UnsentTimedOut > 0 {
			lines = append(lines, fmt.Sprintf("%s: %d calls timed out before going out: they waited on the connection or the\n"+
				"generator, not the target.", displayMethod(m.Method), m.UnsentTimedOut))
		}

		if len(lines) > 0 {
			notes = append(notes, strings.Join(lines, "\n"))
		}
	}

	// The category says whose fault a failure is; the code is what the
	// target's logs call it. A code the client set is kept apart: next to the
	// target's it would read as the target's answer.
	var codeLines []string
	for i := range report.Methods {
		m := &report.Methods[i]
		for _, group := range []struct {
			fromTarget bool
			label      string
		}{{true, "codes sent by the target"}, {false, "codes set by the client"}} {
			var codes []string
			for _, c := range m.FailureCodes {
				if c.FromTarget == group.fromTarget {
					codes = append(codes, fmt.Sprintf("%s %d", c.Code, c.Count))
				}
			}
			if len(codes) > 0 {
				codeLines = append(codeLines, fmt.Sprintf("%s %s: %s", displayMethod(m.Method), group.label, strings.Join(codes, ", ")))
			}
		}
	}
	if len(codeLines) > 0 {
		notes = append(notes, "failed calls by gRPC code:\n"+strings.Join(codeLines, "\n"))
	}

	notes = append(notes, streamNotes(report)...)

	// What the generator did. Named for what it measures: a generator late to
	// pick up answers is not in it, so it does not vouch for the latencies.
	if report.StartLagP99.Defined || report.StartLagMax > 0 {
		lag := fmt.Sprintf("start lag, how late calls began against their schedule: p99 %s, max %s.\n"+
			"It does not see answers picked up late.",
			formatQuantile(report.StartLagP99), formatLatency(report.StartLagMax))
		if report.LateCancelMax > 0 {
			lag += fmt.Sprintf("\nTimeouts returned up to %s past their deadline.", formatLatency(report.LateCancelMax))
		}
		notes = append(notes, lag)
	}

	if len(rejected) > 0 {
		add("The \"request error\" rows are calls that fail the same way at any rate. Either\n"+
			"the request is wrong — no such method, a bad argument, a body that does not match\n"+
			"the schema — or\n"+
			"a request refused as larger than accepted, by the target or a proxy in front of it.\n"+
			"Check the config for %s.", strings.Join(rejected, ", "))
	}

	if report.RequestRejected {
		for i := range report.Methods {
			if note := invalidNote(&report.Methods[i], maxResponse); note != "" {
				add("%s", note)
			}
		}
	}

	var overload, failure, bad int
	for i := range report.Methods {
		overload += report.Methods[i].Overload.Count
		failure += report.Methods[i].Failure.Count
		bad += report.Methods[i].BadResponse.Count
	}
	if overload+failure+bad > 0 {
		rows := []string{"A method's percentiles are the time to serve a call: successes, and timeouts\n" +
			"as lower bounds. The rows under it time the other answers:"}
		if overload > 0 {
			rows = append(rows, "\"overload\": the status says overloaded or unavailable (RESOURCE_EXHAUSTED,\n"+
				"UNAVAILABLE), from the target or a proxy in front of it; a proxy with no live\n"+
				"backend says the same;")
		}
		if failure > 0 {
			rows = append(rows, "\"failure\": the status says the call broke (INTERNAL, UNKNOWN, ABORTED and\n"+
				"others), from the target or a proxy in front of it;")
		}
		if bad > 0 {
			rows = append(rows, "\"bad response\": a reply came and the client did not accept it.")
		}
		notes = append(notes, strings.Join(rows, "\n"))
	}

	// Aborted calls are censored too, but raising the timeout would not show
	// their tail: the stop cut them off, not the deadline.
	if timedOut := censored - report.Aborted; timedOut > 0 {
		add("%d requests were abandoned before answering. A percentile shown as \"> value\"\n"+
			"is a lower bound: the real tail lies above it. Raise the timeout to see it.", timedOut)
	}

	if report.Aborted > 0 {
		add("%d requests were cut off by the abort. They are no fault of the target and are\n"+
			"not counted as failures; each is known only to have lasted until the abort.", report.Aborted)
	}

	if hit := report.CapHit; hit != nil {
		add("invalid run: calls held their slots more than %s (the allowance) past their\n"+
			"deadline, and the in-flight cap was hit at %s. The generator lacked CPU, or the sender\n"+
			"does not honor deadlines; the target is not what filled the cap. At the hit %d slots\n"+
			"were being held past their own deadline; %d call was refused by the cap and\n"+
			"never sent.",
			formatDuration(engine.ReleaseMargin), formatDuration(hit.At), hit.OverDeadline, hit.Unsent)
	}

	if report.Incomplete {
		add("incomplete: the run stopped before its planned end, and ran %s of the planned %s.\n"+
			"The numbers are honest but cover only the part that ran; do not compare them with a\n"+
			"full run.", formatDuration(report.Duration), formatDuration(report.Planned))
	}

	if v := streamVerdict(report); v != "" {
		notes = append(notes, v)
	}

	if cutOff > 0 {
		add("%d calls were cut off after going out: no status came back, and the other end,\n"+
			"the target or a proxy in front of it, may have processed them.", cutOff)
	}

	if unanswered > 0 {
		add("%d requests never reached the target and carry no latency, so they are\n"+
			"counted as failures but left out of the percentiles above.", unanswered)
	}

	if outside > 0 {
		add("warning: %d requests fell outside the per-second timeline and are missing\n"+
			"from it. The generator ran far behind its schedule or a clock jumped; the\n"+
			"totals above still count them.", outside)
	}

	if unclassified > 0 {
		add("warning: %d requests came back without a category and are left out of the\n"+
			"percentiles. This is a bug in the sender, not in the target. Please report it.", unclassified)
	}

	if invalid > 0 {
		add("warning: %d measurements were impossible (negative latency) and left out.\n"+
			"This is a bug in LeetTest, not in the target. Please report it.", invalid)
	}

	return notes
}

// runNotes are reportNotes and what the CLI measured beside the engine: the
// host clock.
func runNotes(run RunReport) []string {
	notes := reportNotes(run.Report, run.MaxResponse)

	if m := coarseClockMethod(run); m != nil {
		notes = append(notes, fmt.Sprintf("invalid run: the clock step on this host (%s) is over a quarter of the p50 of\n"+
			"%s (%s). Each latency is off by up to one step, so the percentiles are\n"+
			"off by over 25%%. Run on a host with a finer clock, such as Linux next to the target.",
			formatLatency(run.ClockStep), strings.TrimPrefix(m.Method, "/"), formatLatency(m.P50.Value)))
	}
	if run.ClockStep >= time.Microsecond {
		line := fmt.Sprintf("clock step %s on this host: every latency and wait is ± %s",
			formatLatency(run.ClockStep), formatLatency(run.ClockStep))
		if run.WaitFloor > engine.StreamWaitFloor {
			line += fmt.Sprintf("; a wait counts from %s, four steps", formatLatency(run.WaitFloor))
		}
		notes = append(notes, line+".")
	}
	if run.TimerNotRaised {
		notes = append(notes, "timer resolution not raised: the host refused a finer timer, and the clock step may\n"+
			"have grown during the run where neither measure of it saw.")
	}

	return notes
}

// coarseClockMethod is the first method whose p50 the clock step is over a
// quarter of, or nil.
func coarseClockMethod(run RunReport) *engine.MethodReport {
	for i := range run.Methods {
		m := &run.Methods[i]
		if m.P50.Defined && 4*run.ClockStep > m.P50.Value {
			return m
		}
	}

	return nil
}

func share(part, whole int) float64 {
	if whole == 0 {
		return 0
	}

	return float64(part) / float64(whole) * 100
}

// invalidNote says why a method measured nothing about load and what to do,
// or "" when it did. Every sent call failed the same way at any rate: the
// request was wrong, the client could not send it, or the client refused the
// replies. Bad responses are told apart by the code the client set.
func invalidNote(m *engine.MethodReport, maxResponse string) string {
	bad := m.BadResponse.Count
	if m.Sent == 0 || m.Rejected.Count+m.ClientError+bad != m.Sent {
		return ""
	}
	name := displayMethod(m.Method)

	switch m.Sent {
	case m.Rejected.Count:
		return fmt.Sprintf("invalid run: every measured call of %s came back as a request that will not\n"+
			"be served, by the target or a proxy in front of it. Nothing about the load was\n"+
			"tested there; fix the request and run again.", name)
	case m.ClientError:
		return fmt.Sprintf("invalid run: the client could not send any call of %s: the request did not\n"+
			"encode, or the client's own stack refused it; see the codes set by the client.\n"+
			"Nothing reached the target.", name)
	case bad:
		oversized := 0
		for _, c := range m.FailureCodes {
			if !c.FromTarget && c.Code == "ResourceExhausted" {
				oversized += c.Count
			}
		}
		switch {
		case oversized == bad && maxResponse != "":
			return fmt.Sprintf("invalid run: every response of %s was larger than max_response_size (%s):\n"+
				"raise it.", name, maxResponse)
		case oversized == bad:
			return fmt.Sprintf("invalid run: every response of %s was larger than 4MiB, the gRPC default:\n"+
				"set app.max_response_size above it.", name)
		case oversized == 0:
			return fmt.Sprintf("invalid run: responses of %s came compressed with an encoding the client does\n"+
				"not accept. gRPC lets a server compress only with an encoding the client\n"+
				"announced in grpc-accept-encoding: the target or a proxy in front of it breaks that.", name)
		}
	}

	return fmt.Sprintf("invalid run: every measured call of %s failed the same way at any rate:\n"+
		"request error %d, client error %d, bad response %d. Nothing about the load was\n"+
		"tested there.", name, m.Rejected.Count, m.ClientError, bad)
}
