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
	"slices"
	"strings"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// streamLimited reports the methods whose printed p99 changes when the stream
// wait is taken out, and whether the run is limited by streams: some printed
// p99 moves, or calls never went out for want of a stream.
func streamLimited(report engine.Report) (moved []*engine.MethodReport, limited bool) {
	for i := range report.Methods {
		m := &report.Methods[i]
		if m.P99WithoutStreamWait.Defined && formatQuantile(m.P99) != formatQuantile(m.P99WithoutStreamWait) {
			moved = append(moved, m)
		}
	}

	return moved, len(moved) > 0 || report.NotSentStream > 0
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}

	return fmt.Sprintf("%d %ss", n, word)
}

// cause is one reason calls waited, with the calls, sent or not, that waited
// over the floor for it.
type cause struct {
	n    int
	what string
}

const (
	causeGenerator  = "generator late"
	causeStream     = "waited for a stream"
	causeConnection = "connection not ready"
)

// rankedCauses lists the causes with calls, largest first; a tie keeps the
// order generator, stream, connection.
func rankedCauses(report engine.Report) []cause {
	var out []cause
	for _, c := range []cause{
		{report.GeneratorCauseCalls, causeGenerator},
		{report.StreamCauseCalls, causeStream},
		{report.ConnectionCauseCalls, causeConnection},
	} {
		if c.n > 0 {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b cause) int { return b.n - a.n })

	return out
}

// verdictCause is the cause that names the verdict, or "" when there is none.
// Whether there is one is decided as before the ranking: streams moved a
// printed p99 or kept calls back, or calls went unsent for the generator or
// the connection. The counts only choose the heading.
func verdictCause(report engine.Report) (cause, bool) {
	_, limited := streamLimited(report)
	if !limited && report.NotSentLate == 0 && report.NotSentConnection == 0 {
		return cause{}, false
	}
	ranked := rankedCauses(report)
	if len(ranked) == 0 {
		return cause{}, false
	}

	return ranked[0], true
}

// streamVerdict is the verdict on a run held back by itself or its
// connection, or "" when nothing moved. It is about the run: the numbers of
// single methods go to streamNotes.
func streamVerdict(report engine.Report) string {
	top, ok := verdictCause(report)
	if !ok {
		return ""
	}

	var b strings.Builder

	switch top.what {
	case causeGenerator:
		fmt.Fprintf(&b, "limited by the run, not the target: the generator fell behind for %s.", plural(top.n, "call"))
	case causeConnection:
		// The target or the network refusing it: not the run's limit.
		fmt.Fprintf(&b, "the connection to the target was not ready for %s.", plural(top.n, "call"))
	default:
		b.WriteString(streamHeading(report))
	}

	// One cause has nothing to rank.
	var parts []string
	if ranked := rankedCauses(report); len(ranked) > 1 {
		for _, c := range ranked {
			parts = append(parts, fmt.Sprintf("%s %d", c.what, c.n))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "\ncauses, largest first: %s.", strings.Join(parts, "; "))
	}

	moved, limited := streamLimited(report)
	if len(moved) > 0 {
		fmt.Fprintf(&b, "\nThe printed p99 includes the wait for %d of %d methods.", len(moved), len(report.Methods))
	}

	if conns := report.Connections; limited && conns != nil && conns.LimitAnnounced {
		inFlight := conns.Open * int(conns.LastLimit)
		fmt.Fprintf(&b, " The target was not tested\nabove %s in flight.", plural(inFlight, "call"))
	}

	return b.String()
}

// streamHeading names the stream limit the run hit.
func streamHeading(report engine.Report) string {
	var b strings.Builder

	b.WriteString("limited by the run, not the target: ")

	waited := fmt.Sprintf("%d of %d sent calls waited for", report.StreamWaited, report.Sent)
	conns := report.Connections

	switch {
	case conns == nil:
		fmt.Fprintf(&b, "%s a stream (p99 %s)", waited, formatQuantile(report.StreamWaitP99))
	case conns.LimitAnnounced:
		fmt.Fprintf(&b, "on %s the target allows %s\n(MAX_CONCURRENT_STREAMS), and %s one (p99 %s)",
			plural(conns.Open, "connection"), plural(int(conns.LastLimit), "stream"),
			waited, formatQuantile(report.StreamWaitP99))
	default:
		fmt.Fprintf(&b, "on %s %s a stream\n(p99 %s), and the target announced no limit",
			plural(conns.Open, "connection"), waited, formatQuantile(report.StreamWaitP99))
	}

	if report.NotSentStream > 0 {
		fmt.Fprintf(&b, ", %d more were not sent", report.NotSentStream)
	}
	b.WriteString(".")

	return b.String()
}

// shortStreamVerdict is streamVerdict in one ASCII phrase.
func shortStreamVerdict(report engine.Report) string {
	top, ok := verdictCause(report)
	if !ok {
		return ""
	}

	switch top.what {
	case causeGenerator:
		return "limited by the run: the generator fell behind"
	case causeConnection:
		return "the connection to the target was not ready"
	}

	conns := report.Connections

	switch {
	case conns == nil:
		return "limited by the run: calls waited for streams"
	case conns.LimitAnnounced:
		return fmt.Sprintf("limited by %s: target allows %s",
			plural(conns.Open, "connection"), plural(int(conns.LastLimit), "stream"))
	default:
		return fmt.Sprintf("limited by %s: no stream limit announced", plural(conns.Open, "connection"))
	}
}

// streamNotes are the facts behind the verdict: the connections, why calls
// did not go out, and what each moved method's p99 is without the wait.
func streamNotes(report engine.Report) []string {
	var notes []string

	if c := report.Connections; c != nil {
		line := fmt.Sprintf("connections: %d", c.Open)
		if c.Reconnects > 0 {
			line += fmt.Sprintf(" (reconnects: %d)", c.Reconnects)
		}

		switch {
		case c.LimitChanges > 0:
			line += fmt.Sprintf("; target stream limit %d to %d, changed %d times", c.FirstLimit, c.LastLimit, c.LimitChanges)
		case c.LimitAnnounced:
			line += fmt.Sprintf("; target stream limit %d", c.LastLimit)
		default:
			line += "; no stream limit announced"
		}

		notes = append(notes, line+".")
	}

	if report.NotSent > 0 {
		var reasons []string
		for _, r := range []struct {
			n    int
			what string
		}{
			{report.NotSentStream, "waited for a stream"},
			{report.NotSentConnection, "waited for the connection"},
			{report.NotSentLate, "generator late"},
		} {
			if r.n > 0 {
				reasons = append(reasons, fmt.Sprintf("%s %d", r.what, r.n))
			}
		}
		notes = append(notes, fmt.Sprintf("not sent %d: %s.", report.NotSent, strings.Join(reasons, ", ")))
	}

	moved, limited := streamLimited(report)
	for _, m := range moved {
		notes = append(notes, fmt.Sprintf("%s: p99 without the stream wait is %s.",
			displayMethod(m.Method), formatQuantile(m.P99WithoutStreamWait)))
	}

	if !limited && report.StreamWaited > 0 {
		notes = append(notes, fmt.Sprintf("%d of %d sent calls waited for a stream (p99 %s); p99 unchanged.",
			report.StreamWaited, report.Sent, formatQuantile(report.StreamWaitP99)))
	}

	return notes
}
