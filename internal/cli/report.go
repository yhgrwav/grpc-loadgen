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
	"io"
	"strings"
	"time"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

// PrintReport writes the finished run to w as plain text.
func PrintReport(w io.Writer, target string, report engine.Report) {
	fmt.Fprintf(w, "run finished: %s in %s\n", target, formatDuration(report.Duration))
	if report.Aborted > 0 {
		fmt.Fprintf(w, "sent %d, failed %d, aborted %d\n\n", report.Sent, report.Failed, report.Aborted)
	} else {
		fmt.Fprintf(w, "sent %d, failed %d\n\n", report.Sent, report.Failed)
	}

	fmt.Fprintf(w, "%-44s %8s %8s %9s %9s %9s %9s %9s\n",
		"method", "sent", "failed", "sent/s", "p50", "p90", "p95", "p99")

	censored, invalid, unanswered, unclassified, outside, refused := 0, 0, 0, 0, 0, 0
	rejected := make([]string, 0, len(report.Methods))
	outright := make([]string, 0, len(report.Methods))

	for i := range report.Methods {
		m := &report.Methods[i]
		censored += m.Censored
		invalid += m.Invalid
		unanswered += m.Unanswered
		unclassified += m.Unclassified
		outside += m.OutsideTimeline
		refused += m.Refusal.Count

		fmt.Fprintf(w, "%-44s %8d %8d %9.0f %9s %9s %9s %9s\n",
			displayMethod(m.Method), m.Sent, m.Failed, m.RPS,
			formatQuantile(m.P50), formatQuantile(m.P90), formatQuantile(m.P95), formatQuantile(m.P99))

		if r := m.Rejected; r.Count > 0 {
			rejected = append(rejected, displayMethod(m.Method))
			if r.Count == m.Sent {
				outright = append(outright, displayMethod(m.Method))
			}
			fmt.Fprintf(w, "%-44s %8s %8d %9s %9s %9s %9s %9s\n", "  rejected", "", r.Count, "",
				formatQuantile(r.P50), formatQuantile(r.P90), formatQuantile(r.P95), formatQuantile(r.P99))
		}

		if r := m.Refusal; r.Count > 0 {
			fmt.Fprintf(w, "%-44s %8s %8d %9s %9s %9s %9s %9s\n", "  refused", "", r.Count, "",
				formatQuantile(r.P50), formatQuantile(r.P90), formatQuantile(r.P95), formatQuantile(r.P99))
		}
	}

	printNoAnswer(w, report.Methods)
	printStartLag(w, report)

	if len(rejected) > 0 {
		fmt.Fprintf(w, "\nThe \"rejected\" rows are calls that fail the same way at any rate. Either the\n"+
			"request is wrong — no such method, a bad argument, a body that does not match\n"+
			"the schema — or a message did not fit: a reply rejected by the client's 4MB limit,\n"+
			"or a request the target refused as larger than it accepts. Check the config for\n"+
			"%s.\n", strings.Join(rejected, ", "))
	}

	if report.RequestRejected {
		fmt.Fprintf(w, "\ninvalid run: every measured call of %s came back as a request the target\n"+
			"will not serve. Nothing about the load was tested there; fix the request and run\n"+
			"again.\n", strings.Join(outright, ", "))
	}
	if refused > 0 {
		fmt.Fprint(w, "\nA method's percentiles are the time to serve a call: successes, and timeouts\n"+
			"as lower bounds. The \"refused\" rows are how long the target took to say no.\n")
	}

	// Aborted calls are censored too, but raising the timeout would not show
	// their tail: the stop cut them off, not the deadline.
	if timedOut := censored - report.Aborted; timedOut > 0 {
		fmt.Fprintf(w, "\n%d requests were abandoned before answering. A percentile shown as "+
			"\"> value\"\nis a lower bound: the real tail lies above it. Raise the timeout to see it.\n",
			timedOut)
	}

	if report.Aborted > 0 {
		fmt.Fprintf(w, "\n%d requests were cut off by the abort. They are no fault of the target and are\n"+
			"not counted as failures; each is known only to have lasted until the abort.\n", report.Aborted)
	}

	if hit := report.CapHit; hit != nil {
		fmt.Fprintf(w, "\ninvalid run: calls held their slots more than %s (the allowance) past their\n"+
			"deadline, and the in-flight cap was hit at %s. The generator lacked CPU, or the sender\n"+
			"does not honor deadlines; the target is not what filled the cap. At the hit %d slots\n"+
			"were being held past their own deadline; %d call was refused by the cap and\n"+
			"never sent.\n",
			formatDuration(engine.ReleaseMargin), formatDuration(hit.At), hit.OverDeadline, hit.Unsent)
	}

	if report.Incomplete {
		fmt.Fprintf(w, "\nincomplete: the run stopped before its planned end, and ran %s of the planned %s.\n"+
			"The numbers are honest but cover only the part that ran; do not compare them with a\n"+
			"full run.\n", formatDuration(report.Duration), formatDuration(report.Planned))
	}

	if unanswered > 0 {
		fmt.Fprintf(w, "\n%d requests never reached the target and carry no latency, so they are\n"+
			"counted as failures but left out of the percentiles above.\n", unanswered)
	}

	if outside > 0 {
		fmt.Fprintf(w, "\nwarning: %d requests fell outside the per-second timeline and are missing\n"+
			"from it. The generator ran far behind its schedule or a clock jumped; the\n"+
			"totals above still count them.\n", outside)
	}

	if unclassified > 0 {
		fmt.Fprintf(w, "\nwarning: %d requests came back without a category and are left out of the\n"+
			"percentiles. This is a bug in the sender, not in the target. Please report it.\n", unclassified)
	}

	if invalid > 0 {
		fmt.Fprintf(w, "\nwarning: %d measurements were impossible (negative latency) and left out.\n"+
			"This is a bug in LeetTest, not in the target. Please report it.\n", invalid)
	}
}

// formatQuantile prints a percentile the way it is known: an exact value, a
// lower bound when the tail ran past the timeout, or a dash when nothing was
// measured at all.
func formatQuantile(q metrics.Quantile) string {
	if !q.Defined {
		return "-"
	}
	if !q.Exact {
		return ">" + formatDuration(q.Value)
	}

	return formatDuration(q.Value)
}

func formatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fus", float64(d.Nanoseconds())/1e3)
	case d < time.Second:
		return fmt.Sprintf("%.0fms", float64(d.Nanoseconds())/1e6)
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// printNoAnswer states, per method, how many calls went out and got no answer
// within their timeout, and from which second on none did. It states what was
// measured and nothing more: whether that means the target cannot take the
// rate needs a threshold, which is not this report's to pick.
func printNoAnswer(w io.Writer, methods []engine.MethodReport) {
	for i := range methods {
		m := &methods[i]
		if m.TimedOut == 0 && m.UnsentTimedOut == 0 {
			continue
		}

		rate := fmt.Sprintf("%d", m.RPSLow)
		if m.RPSHigh != m.RPSLow {
			rate = fmt.Sprintf("%d-%d", m.RPSLow, m.RPSHigh)
		}

		if m.TimedOut > 0 {
			fmt.Fprintf(w, "\n%s: at %s rps, %d of %d calls (%.1f%%) got no answer within %s",
				displayMethod(m.Method), rate, m.TimedOut, m.Sent, share(m.TimedOut, m.Sent), formatDuration(m.Timeout))
			if m.SilentFrom != nil {
				fmt.Fprintf(w, ",\nand from second %d on none did (seconds by schedule, warmup counted)", *m.SilentFrom)
			}
			fmt.Fprint(w, ".\n")
		}

		if m.UnsentTimedOut > 0 {
			fmt.Fprintf(w, "%s: %d calls timed out before going out: they waited on the connection or the\n"+
				"generator, not the target.\n", displayMethod(m.Method), m.UnsentTimedOut)
		}
	}
}

func share(part, whole int) float64 {
	if whole == 0 {
		return 0
	}

	return float64(part) / float64(whole) * 100
}

// printStartLag says how late calls started against their schedule. It is
// named for what it measures: a generator late to pick up answers is not in
// it, so it does not vouch for the latencies.
func printStartLag(w io.Writer, report engine.Report) {
	if !report.StartLagP99.Defined && report.StartLagMax == 0 {
		return
	}

	fmt.Fprintf(w, "\nstart lag, how late calls began against their schedule: p99 %s, max %s.\n"+
		"It does not see answers picked up late.\n",
		formatQuantile(report.StartLagP99), formatDuration(report.StartLagMax))
	if report.LateCancelMax > 0 {
		fmt.Fprintf(w, "Timeouts returned up to %s past their deadline.\n", formatDuration(report.LateCancelMax))
	}
}
