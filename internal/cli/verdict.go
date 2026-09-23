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
		for i := range report.Methods {
			r := &report.Methods[i]
			if r.Rejected.Count > 0 && r.Rejected.Count == r.Sent {
				outright = append(outright, displayMethod(r.Method))
			}
		}
		if len(outright) == 1 {
			const head, tail = "invalid run: every call of ", " was rejected"
			// One line: at 60x16 a second one would take the table's only row.
			out = append(out, head+truncateLeft(ascii(outright[0]), room-len(head)-len(tail))+tail)
		} else {
			out = append(out, fmt.Sprintf("invalid run: every call of %d methods was rejected", len(outright)))
		}
	}
	if hit := report.CapHit; hit != nil {
		out = append(out, "invalid run: in-flight cap hit at "+formatDuration(hit.At))
	}
	if report.Incomplete {
		out = append(out, fmt.Sprintf("incomplete: ran %s of the planned %s",
			formatDuration(report.Duration), formatDuration(report.Planned)))
	}
	if m.err != nil && !m.stopper.Stopping() {
		// The reason in one phrase: an error reads outermost first.
		reason, _, _ := strings.Cut(m.err.Error(), ": ")
		out = append(out, truncate("run failed: "+ascii(reason), room))
	}

	return out
}

// ascii replaces what a terminal may draw at another width with "?".
func ascii(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 127 {
			return '?'
		}

		return r
	}, s)
}
