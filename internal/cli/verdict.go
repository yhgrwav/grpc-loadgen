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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// shortVerdicts are the final screen's verdicts in one phrase each, in the
// order of the full ones: ASCII, and at most two lines of width less the
// note's mark.
func (m *model) shortVerdicts(width int) []string {
	report := m.report
	room := width - 2

	var out []string
	if report.RequestRejected {
		var outright []string
		tail := ""
		for i := range report.Methods {
			r := &report.Methods[i]
			if invalidNote(r, "") == "" {
				continue
			}
			outright = append(outright, displayMethod(r.Method))
			// One tail for all of them, or the generic one when they differ.
			if t := invalidTail(r); tail == "" || tail == t {
				tail = t
			} else {
				tail = " failed at any rate"
			}
		}
		switch {
		case len(outright) == 1 && isASCII(outright[0]):
			const head = "invalid run: every call of "
			// One line: at 60x16 a second one would take the table's only row.
			out = append(out, head+truncateLeft(outright[0], room-len(head)-len(tail))+tail)
		case len(outright) == 1:
			out = append(out, "invalid run: every call of 1 method"+tail)
		default:
			out = append(out, fmt.Sprintf("invalid run: every call of %d methods%s", len(outright), tail))
		}
	}
	if hit := report.CapHit; hit != nil {
		out = append(out, "invalid run: in-flight cap hit at "+formatDuration(hit.At))
	}
	if report.Incomplete {
		out = append(out, fmt.Sprintf("incomplete: ran %s of the planned %s",
			formatDuration(report.Duration), formatDuration(report.Planned)))
	}
	if v := shortStreamVerdict(report); v != "" {
		out = append(out, v)
	}
	if m.err != nil && !m.stopper.Stopping() {
		out = append(out, truncate("run failed: "+failureReason(m.err, room-len("run failed: ")), room))
	}

	return out
}

// failureReason is why a run failed, in at most room columns of ASCII: a gRPC
// status by its code and, room allowing, its description; otherwise the
// context LeetTest wrapped the error in. Text in another script is not cut to
// "?" but left to stderr, where the error is printed after exit.
func failureReason(err error, room int) string {
	const elsewhere = "details are printed after exit"

	text := err.Error()
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		reason := st.Code().String()
		// What LeetTest put around the status: "connect to host:port".
		if context, _, found := strings.Cut(text, "rpc error:"); found {
			if context = strings.TrimSuffix(strings.TrimSpace(context), ":"); context != "" && isASCII(context) {
				// The code stays whole; the context gives way from its head.
				reason = truncateLeft(context, room-len(reason)-2) + ": " + reason
			}
		}
		if desc := st.Message(); desc != "" && isASCII(desc) && len(reason)+2+len(desc) <= room {
			reason += ": " + desc
		}

		return reason
	}

	reason, _, _ := strings.Cut(text, ": ")
	if !isASCII(reason) {
		return elsewhere
	}

	return reason
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}

	return true
}

// invalidTail ends the short verdict for a method invalidNote speaks of.
func invalidTail(m *engine.MethodReport) string {
	switch m.Sent {
	case m.Rejected.Count:
		return " was rejected"
	case m.ClientError:
		return " failed to send"
	case m.BadResponse.Count:
		return " got bad replies"
	default:
		return " failed at any rate"
	}
}
