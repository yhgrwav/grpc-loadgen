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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// A report whose every table row and column carries a value no other cell
// repeats, so a cell found on the screen is that cell and not a neighbour.
func tableReport() engine.Report {
	return engine.Report{
		// 1000 sent over 12s is 83/s; the rate over the sending window is 97.
		// A screen dividing by the run's length shows the first.
		Duration: 12 * time.Second, Sent: 1000, Failed: 150,
		Methods: []engine.MethodReport{{
			Method: "pkg.Svc/One", Sent: 1000, Failed: 150, RPS: 97,
			P50: exact(11), P90: exact(12),
			P95: exact(13), P99: exact(14),
			Refusal: engine.RefusalLatency{Count: 100,
				P50: exact(21), P90: exact(22),
				P95: exact(23), P99: exact(24)},
			Rejected: engine.RefusalLatency{Count: 50,
				P50: exact(31), P90: exact(32),
				P95: exact(33), P99: exact(34)},
		}},
	}
}

// tableRows is the text report's table: one row per line between the heading
// and the first blank line, as fields.
func tableRows(text string) [][]string {
	var rows [][]string
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "method ") {
			continue
		}
		for _, row := range lines[i+1:] {
			if strings.TrimSpace(row) == "" {
				break
			}
			rows = append(rows, strings.Fields(row))
		}

		break
	}

	return rows
}

// Ground: contract — the final screen is built from the same report as the
// text one (decisions: one report, two renderings). A row the log has and the
// screen lacks, or a number that differs, tells the two readers different
// things about one run.
func TestFinalScreenTableCarriesEveryRowAndNumberOfTheTextReport(t *testing.T) {
	report := tableReport()

	var text strings.Builder
	PrintReport(&text, "localhost:50051", report)
	rows := tableRows(text.String())
	if len(rows) != 3 {
		t.Fatalf("the text report's table has %d rows, want 3: the test no longer compares:\n%s",
			len(rows), text.String())
	}

	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.done, m.report = true, report
	screen := strings.Split(m.finalReport(contentWidth(120)), "\n")

	for _, row := range rows {
		label := row[0]
		if label == "pkg.Svc/One" {
			label = shortMethod(label)
		}

		var found []string
		for _, line := range screen {
			if f := strings.Fields(line); len(f) > 0 && f[0] == label {
				found = f
				break
			}
		}
		if found == nil {
			t.Errorf("the screen has no %q row; the text report has %v", row[0], row)
			continue
		}
		for _, cell := range row[1:] {
			if !strings.Contains(" "+strings.Join(found, " ")+" ", " "+cell+" ") {
				t.Errorf("the screen's %q row lacks %q: screen %v, text %v", row[0], cell, found, row)
			}
		}
	}
}

// Ground: boundary — the alternate screen shows the last lines of a view
// taller than the terminal and drops the top ones without a word (TASK,
// «Терминал ниже содержимого»). The table must not be what goes: the notes
// give way first, and the screen says how many lines it left out and where
// they are.
func TestFinalScreenOnAShortTerminalKeepsTheTableAndSaysWhatItCut(t *testing.T) {
	report := tableReport()
	for i := range 5 {
		report.Methods = append(report.Methods, engine.MethodReport{
			Method: "pkg.Svc/M" + string(rune('a'+i)), Sent: 10, Failed: 10, TimedOut: 10,
			RPSLow: 1, RPSHigh: 1, Timeout: time.Second,
		})
	}

	const height = 16

	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: height})
	m.done, m.report = true, report

	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines > height {
		t.Errorf("the view is %d lines on a %d-line terminal: the top of it is lost unseen", lines, height)
	}
	for i := range report.Methods {
		if name := shortMethod(report.Methods[i].Method); !strings.Contains(view, name) {
			t.Errorf("the %s row is not on the screen", name)
		}
	}
	if !strings.Contains(view, "printed after exit") {
		t.Errorf("the screen does not say that the rest of the report is printed after exit:\n%s", view)
	}
}
