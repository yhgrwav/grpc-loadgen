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
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

// RunReport is the engine's report with what only the CLI knows about the
// run: the methods nothing could be checked against before it,
// and the reply size limit as the config wrote it, empty for the gRPC default.
type RunReport struct {
	engine.Report
	Unchecked   []Unchecked
	MaxResponse string
	// ClockStep is the larger of the host clock's steps measured before and
	// after the run.
	ClockStep time.Duration
	// TimerNotRaised says the host refused a finer timer: the step may have
	// coarsened mid-run where neither measure saw it.
	TimerNotRaised bool
}

// ClockTooCoarse says the clock step is over a quarter of some method's p50.
func ClockTooCoarse(run RunReport) bool {
	return coarseClockMethod(run) != nil
}

// PrintReport writes the finished run to w as plain text.
func PrintReport(w io.Writer, target string, run RunReport) {
	report := run.Report

	fmt.Fprintf(w, "run finished: %s in %s\n", target, formatDuration(report.Duration))
	fmt.Fprintf(w, "sent %d, failed %d", report.Sent, report.Failed)
	if report.NotSent > 0 {
		fmt.Fprintf(w, ", not sent %d", report.NotSent)
	}
	if report.Aborted > 0 {
		fmt.Fprintf(w, ", aborted %d", report.Aborted)
	}
	fmt.Fprint(w, "\n\n")

	fmt.Fprintf(w, "%-44s %8s %8s %9s %9s %9s %9s %9s\n",
		"method", "sent", "failed", "sent/s", "p50", "p90", "p95", "p99")

	for i := range report.Methods {
		m := &report.Methods[i]

		fmt.Fprintf(w, "%-44s %8d %8d %9.0f %9s %9s %9s %9s\n",
			displayMethod(m.Method), m.Sent, m.Failed, m.RPS,
			formatQuantile(m.P50), formatQuantile(m.P90), formatQuantile(m.P95), formatQuantile(m.P99))

		for _, sub := range answerRows(m) {
			r := sub.r
			fmt.Fprintf(w, "%-44s %8s %8d %9s %9s %9s %9s %9s\n", "  "+sub.label, "", r.Count, "",
				formatQuantile(r.P50), formatQuantile(r.P90), formatQuantile(r.P95), formatQuantile(r.P99))
		}
	}

	for _, note := range runNotes(run) {
		fmt.Fprintf(w, "\n%s\n", note)
	}
	if note := uncheckedNote(run.Unchecked); note != "" {
		fmt.Fprintf(w, "\n%s\n", note)
	}
}

// uncheckedNote names the methods nothing could be checked against before
// the run. It goes with the report rather than only into the progress output:
// a warning printed before a full-screen run is gone by the time the numbers
// are read, and a typo in one of these methods shows up above only as
// failures.
func uncheckedNote(methods []Unchecked) string {
	if len(methods) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("not checked before the run:")
	for _, m := range methods {
		// "off" only when the target said it does not implement reflection.
		// Refused, timed out or answered with something else is a different
		// fact, and naming it "off" would send the reader to the wrong place.
		why := fmt.Sprintf("server reflection could not be used: %v", m.Err)
		if errors.Is(m.Err, descriptor.ErrReflectionUnsupported) {
			why = "server reflection is off on the target"
		}

		fmt.Fprintf(&b, "\n  %s: %s", displayMethod(m.Method), why)
	}
	b.WriteString("\nA method that does not exist on the target is then seen only as the failures\nabove.")

	return b.String()
}

// formatQuantile prints a percentile the way it is known: an exact value, a
// lower bound when the tail ran past the timeout, or a dash when nothing was
// measured at all.
func formatQuantile(q metrics.Quantile) string {
	if !q.Defined {
		return "-"
	}
	if !q.Exact {
		// A lower bound is rounded down: rounded up it would state more than
		// is known.
		return ">" + formatLatencyWith(q.Value, scaledDown)
	}

	return formatLatency(q.Value)
}

// formatLatency prints a latency with the 3 significant figures the
// histograms keep: 312us, 10.3ms, 1.71s, 12.3s, then minutes and seconds.
func formatLatency(d time.Duration) string {
	return formatLatencyWith(d, scaled)
}

// formatLatencyWith is formatLatency with the rounding given: scaled for a
// measured value, scaledDown for a lower bound.
func formatLatencyWith(d time.Duration, scale func(n, unit int64, dec int) int64) string {
	n := d.Nanoseconds()
	switch {
	case n < 0:
		return "-" + formatLatencyWith(-d, scale)
	case n == 0:
		return "0"
	case n < 1000:
		return fmt.Sprintf("%dns", n)
	}

	for _, u := range []struct {
		size int64
		name string
	}{{1e3, "us"}, {1e6, "ms"}} {
		for dec := 2; dec >= 0; dec-- {
			if v := scale(n, u.size, dec); v < 1000 {
				return fixed(v, dec) + u.name
			}
		}
	}
	if v := scale(n, 1e9, 2); v < 1000 {
		return fixed(v, 2) + "s"
	}
	if v := scale(n, 1e9, 1); v < 600 {
		return fixed(v, 1) + "s"
	}

	secs := scale(n, 1e9, 0)

	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

// formatDuration prints a length of time: to the microsecond below a
// millisecond, the millisecond below a second, the tenth below a minute.
func formatDuration(d time.Duration) string {
	n := d.Nanoseconds()
	switch {
	case n < 0:
		return "-" + formatDuration(-d)
	case n == 0:
		return "0"
	case n < 1000:
		return fmt.Sprintf("%dns", n)
	}

	if v := scaled(n, 1e3, 0); v < 1000 {
		return fmt.Sprintf("%dus", v)
	}
	if v := scaled(n, 1e6, 0); v < 1000 {
		return fmt.Sprintf("%dms", v)
	}

	return formatSeconds(n)
}

// formatSeconds prints n nanoseconds to the tenth of a second below a minute,
// to the second from then on. Both round: 59.96s is 1m00s.
func formatSeconds(n int64) string {
	if v := scaled(n, 1e9, 1); v < 600 {
		return fixed(v, 1) + "s"
	}

	secs := scaled(n, 1e9, 0)

	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

// scaled is n in units of unit with dec decimals, as an integer rounded half
// away from zero: 10.35ms at 1e6 and 1 is 104. Integer arithmetic, since a
// float of 10.35 is 10.3499… and would round down.
func scaled(n, unit int64, dec int) int64 {
	k := int64(1)
	for range dec {
		k *= 10
	}

	return (n*k + unit/2) / unit
}

// scaledDown is scaled rounded down, for a lower bound.
func scaledDown(n, unit int64, dec int) int64 {
	k := int64(1)
	for range dec {
		k *= 10
	}

	return n * k / unit
}

// fixed writes v, holding dec decimals, as a number: 104 with 1 is 10.4.
func fixed(v int64, dec int) string {
	if dec == 0 {
		return strconv.FormatInt(v, 10)
	}

	k := int64(1)
	for range dec {
		k *= 10
	}

	return fmt.Sprintf("%d.%0*d", v/k, dec, v%k)
}

type answerRow struct {
	label string
	r     engine.RefusalLatency
}

// answerRows are a method's answers other than a success, each timed apart,
// in a fixed order and only when there are any.
func answerRows(m *engine.MethodReport) []answerRow {
	var rows []answerRow
	for _, row := range []answerRow{
		{"request error", m.Rejected}, {"overload", m.Overload}, {"failure", m.Failure}, {"bad response", m.BadResponse},
	} {
		if row.r.Count > 0 {
			rows = append(rows, row)
		}
	}

	return rows
}
