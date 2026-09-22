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
		"method", "sent", "failed", "rps", "p50", "p90", "p95", "p99")

	censored, invalid, unanswered, outside := 0, 0, 0, 0

	for i := range report.Methods {
		m := &report.Methods[i]
		censored += m.Censored
		invalid += m.Invalid
		unanswered += m.Unanswered
		outside += m.OutsideTimeline

		fmt.Fprintf(w, "%-44s %8d %8d %9.0f %9s %9s %9s %9s\n",
			displayMethod(m.Method), m.Sent, m.Failed, m.RPS,
			formatQuantile(m.P50), formatQuantile(m.P90), formatQuantile(m.P95), formatQuantile(m.P99))
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

	if report.Incomplete {
		fmt.Fprint(w, "\nincomplete: the run stopped before its planned end. The numbers are honest but\n"+
			"cover only the part that ran; do not compare them with a full run.\n")
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
