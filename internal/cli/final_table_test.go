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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// A report whose every table row and column carries a value no other cell
// repeats, so a cell found on the screen is that cell and not a neighbour.
// It is also the ordinary numbers the 64-column boundary is set on: counts of
// 1000, 400, 150, 100, 50 and 7, rates of 97 and 40, latencies of 8ms to
// 41ms. None is
// wider than its column's floor, so the seven columns take their floors:
// 5 + 6 + 6 + 4x6 plus a space each is 48, and a 64-column terminal leaves
// 56 inside the frame: 8 for the name.
func tableReport() engine.Report {
	return engine.Report{
		// The totals are the sum of the methods, as the engine makes them.
		// 1400 sent over 12s is 117/s, a rate no row has: a screen dividing
		// by the run's length shows it.
		Duration: 12 * time.Second, Sent: 1400, Failed: 157,
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
		}, {
			Method: "pkg.Svc/Two", Sent: 400, Failed: 7, RPS: 40,
			P50: exact(8), P90: exact(19), P95: exact(26), P99: exact(41),
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
	PrintReport(&text, "localhost:50051", RunReport{Report: report})
	rows := tableRows(text.String())
	if len(rows) != 4 {
		t.Fatalf("the text report's table has %d rows, want 4: the test no longer compares:\n%s",
			len(rows), text.String())
	}

	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.done, m.report = true, report
	screen := strings.Split(m.finalReport(contentWidth(120)), "\n")

	// The stdout report has no run-wide rate, so the screen shows none: a rate
	// over the run's length (117 here) would be a second, wrong sent/s.
	for _, line := range screen {
		if f := strings.Fields(line); len(f) > 0 && strings.Contains(line, "117") {
			t.Errorf("the screen shows a rate the text report does not have: %q", line)
		}
	}

	for _, row := range rows {
		label := row[0]
		if strings.HasPrefix(label, "pkg.Svc/") {
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
// «Терминал ниже содержимого»). What gives way is fixed: the verdict never,
// and it stands above the table; then the notes; then table rows, with a
// count of those left out; and the screen says where the rest is.
func TestFinalScreenOnAShortTerminalKeepsTheVerdictAndSaysWhatItCut(t *testing.T) {
	report := tableReport()
	report.CapHit = &engine.CapHit{At: time.Second, Unsent: 1, OverDeadline: 3}
	for i := range 20 {
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

	verdict, table := strings.Index(view, "invalid run"), strings.Index(view, "method")
	switch {
	case verdict < 0:
		t.Errorf("the verdict is not on the screen:\n%s", view)
	case table >= 0 && verdict > table:
		t.Errorf("the verdict stands below the table:\n%s", view)
	}
	if !strings.Contains(view, "more methods") {
		t.Errorf("rows left out are not counted:\n%s", view)
	}
	if !strings.Contains(view, "the full report is printed after exit") {
		t.Errorf("the screen does not say where the rest is:\n%s", view)
	}
}

// Ground: boundary — below the height of a heading, the verdict and one row,
// no part of the report can be shown honestly, so none is.
func TestFinalScreenTooShortForAnyRowSaysSo(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 5})
	m.done, m.report = true, tableReport()

	if view := m.View(); !strings.Contains(view, "terminal too small, the full report is printed after exit") {
		t.Errorf("a 5-line terminal does not say it is too small:\n%s", view)
	}
}

// Ground: contract — latencies are never dropped (decisions): a name too long
// for the column gives way instead, from its head, since methods of one
// service differ in their tail.
func TestFinalScreenCutsALongNameFromTheHead(t *testing.T) {
	report := tableReport()
	report.Methods[0].Method = "pkg.Svc/AVeryLongMethodNameThatEndsInHistory"

	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m.done, m.report = true, report

	var row string
	for _, line := range strings.Split(m.finalReport(contentWidth(80)), "\n") {
		if strings.Contains(line, "InHistory") || strings.Contains(line, "AVery") {
			row = line
			break
		}
	}
	if !strings.Contains(row, "...") || !strings.Contains(row, "EndsInHistory") {
		t.Errorf("the name is not cut from the head with an ASCII mark: %q", row)
	}
	for _, cell := range []string{"11ms", "12ms", "13ms", "14ms"} {
		if !strings.Contains(row, cell) {
			t.Errorf("the row at 80 columns lacks %s: %q", cell, row)
		}
	}
}

// Ground: boundary — the name gets its own line when fewer than 8 columns are
// left for it next to the seven numbers; in English that happens below 64
// columns. Either way no number is lost.
func TestFinalScreenPutsTheNameOnItsOwnLineBelow64Columns(t *testing.T) {
	numbers := []string{"1000", "150", "97", "11ms", "12ms", "13ms", "14ms"}
	hasAll := func(line string) bool {
		f := " " + strings.Join(strings.Fields(line), " ") + " "
		for _, n := range numbers {
			if !strings.Contains(f, " "+n+" ") {
				return false
			}
		}

		return true
	}

	for _, tc := range []struct {
		width   int
		twoRows bool
	}{{60, true}, {63, true}, {64, false}} {
		t.Run(strconv.Itoa(tc.width), func(t *testing.T) {
			m := testModel(t)
			m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 40})
			m.done, m.report = true, tableReport()
			lines := strings.Split(m.finalReport(contentWidth(tc.width)), "\n")

			name := -1
			for i, line := range lines {
				if f := strings.Fields(line); len(f) > 0 && f[0] == shortMethod("pkg.Svc/One") {
					name = i
					break
				}
			}
			if name < 0 {
				t.Fatalf("no row for the method:\n%s", strings.Join(lines, "\n"))
			}

			switch {
			case tc.twoRows && (len(strings.Fields(lines[name])) != 1 || name+1 >= len(lines) || !hasAll(lines[name+1])):
				t.Errorf("want the name alone and all seven numbers on the next line:\n%s", strings.Join(lines, "\n"))
			case !tc.twoRows && !hasAll(lines[name]):
				t.Errorf("want the name and all seven numbers on one line:\n%s", strings.Join(lines, "\n"))
			}
		})
	}
}

// rowsAfter returns the n lines after the method's name line, as fields.
func rowsAfter(t *testing.T, screen, name string, n int) [][]string {
	t.Helper()

	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		if f := strings.Fields(line); len(f) == 1 && f[0] == name && i+n < len(lines) {
			out := make([][]string, n)
			for j := range n {
				out[j] = strings.Fields(lines[i+1+j])
			}

			return out
		}
	}
	t.Fatalf("no line with %q alone and %d after it:\n%s", name, n, screen)

	return nil
}

// Ground: boundary — when not even the seven numbers fit one line, the counts
// and the latencies take a line each; with the widest values every column is
// 8: 3x8 + 2 = 26 and 4x8 + 3 = 35, both within the 52 a 60-column terminal
// leaves.
func TestFinalScreenSplitsTheNumbersWhenTheyDoNotFitOneLine(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})
	m.done, m.report = true, widestReport()

	rows := rowsAfter(t, m.finalReport(contentWidth(minWidth)), shortMethod("/wallet.v1.WalletService/GetBalanceWithAVeryLongNameIndeed"), 2)
	if len(rows[0]) != 3 || len(rows[1]) != 4 {
		t.Errorf("want 3 counts, then 4 latencies; got %v", rows)
	}
	for _, cell := range rows[1] {
		if cell != ">999m59s" {
			t.Errorf("latency %q, want >999m59s", cell)
		}
	}
}

// Ground: contract — Russian heads the sent column with "отпр.", so the
// ordinary numbers keep all seven cells at 60 columns.
func TestFinalScreenInRussianKeepsEveryNumberAt60Columns(t *testing.T) {
	m := testModel(t)
	m.text = NewText(LangRU)
	m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})
	m.done, m.report = true, tableReport()

	screen := m.finalReport(contentWidth(minWidth))
	row := rowsAfter(t, screen, shortMethod("pkg.Svc/One"), 1)[0]
	if got := strings.Join(row, " "); got != "1000 150 97 11ms 12ms 13ms 14ms" {
		t.Errorf("numbers %q, want all seven", got)
	}
	if !strings.Contains(screen, "отпр.") || strings.Contains(screen, "отправлено ") {
		t.Errorf("the sent column is not headed отпр.:\n%s", screen)
	}
}

// Ground: boundary — at no height and width is the final screen taller than
// the terminal, in any language, with the widest values and a verdict.
func TestFinalScreenNeverOutgrowsTheTerminal(t *testing.T) {
	report := widestReport()
	report.CapHit = &engine.CapHit{At: time.Second, Unsent: 1, OverDeadline: 3}
	report.Incomplete = true
	for _, lang := range allLangs {
		for _, width := range []int{minWidth, 64, 80, 120} {
			for height := 7; height <= 40; height++ {
				m := testModel(t)
				m.text = NewText(lang)
				m.Update(tea.WindowSizeMsg{Width: width, Height: height})
				m.done, m.report = true, report
				if lines := strings.Count(m.View(), "\n") + 1; lines > height {
					t.Errorf("%s %dx%d: the view is %d lines", lang, width, height, lines)
				}
			}
		}
	}
}

// Ground: contract — one table, two renderings: the screen names its columns
// and totals with the words the text report uses, and its totals are the sum
// of its method rows. A total taken from one method would pass a check of the
// rows alone.
func TestFinalScreenUsesTheTextReportsWordsAndItsTotalsAddUp(t *testing.T) {
	report := tableReport()

	var text strings.Builder
	PrintReport(&text, "localhost:50051", RunReport{Report: report})
	var textHeader []string
	for line := range strings.Lines(text.String()) {
		if strings.HasPrefix(line, "method ") {
			textHeader = strings.Fields(line)
		}
	}

	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.done, m.report = true, report
	screen := m.finalReport(contentWidth(120))

	var header []string
	sent, failed := 0, 0
	for line := range strings.Lines(screen) {
		f := strings.Fields(line)
		switch {
		case len(f) > 0 && f[0] == "method":
			header = f
		case len(f) == 8 && (f[0] == "One" || f[0] == "Two"):
			sent += atoi(t, f[1])
			failed += atoi(t, f[2])
		}
	}
	if strings.Join(header, " ") != strings.Join(textHeader, " ") {
		t.Errorf("screen heading %v, text heading %v", header, textHeader)
	}
	if sent != 1400 || failed != 157 {
		t.Errorf("method rows add up to sent %d, failed %d; want 1400 and 157", sent, failed)
	}
	for _, want := range []string{fmt.Sprintf("sent %d ", sent), fmt.Sprintf("failed %d ", failed)} {
		if !strings.Contains(screen+" ", want) {
			t.Errorf("the screen's totals lack %q: they are not the sum of its rows:\n%s", want, screen)
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()

	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%q is not a count: %v", s, err)
	}

	return n
}

// Ground: contract — the final screen switches nothing between tabs, so it
// does not offer to; the way out stays.
func TestFinalScreenOffersNoTabs(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.done, m.report = true, tableReport()

	footer := m.footer()
	if strings.Contains(footer, m.text.HintTabs()) {
		t.Errorf("the final screen offers tabs: %q", footer)
	}
	if !strings.Contains(footer, m.text.PressToExit()) {
		t.Errorf("the final screen does not say how to leave: %q", footer)
	}
}

// ansiCodes are the colours and styles in a rendered view.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// verdictCases are the run's verdicts, each with its short form and a phrase
// only its full form has.
func verdictCases() []struct {
	name, short, fullOnly string
	setup                 func(*model)
} {
	rejectAll := func(m *model, n int) {
		for i := range n {
			m.report.Methods = append(m.report.Methods, engine.MethodReport{
				Method: "pkg.Svc/Bad" + strconv.Itoa(i), Sent: 20, Failed: 20,
				Rejected: engine.RefusalLatency{Count: 20, P50: exact(1), P90: exact(1), P95: exact(1), P99: exact(1)},
			})
		}
		m.report.RequestRejected = true
	}

	return []struct {
		name, short, fullOnly string
		setup                 func(*model)
	}{
		{"cap", "invalid run: in-flight cap hit at 1.0s", "the allowance", func(m *model) {
			m.report.CapHit = &engine.CapHit{At: time.Second, Unsent: 1, OverDeadline: 3}
		}},
		{"one method rejected", "invalid run: every call of ...vc/Bad0 was rejected", "fix the request", func(m *model) {
			rejectAll(m, 1)
		}},
		{"three methods rejected", "invalid run: every call of 3 methods was rejected", "fix the request", func(m *model) {
			rejectAll(m, 3)
		}},
		{"incomplete", "incomplete: ran 12.0s of the planned 20.0s", "do not compare", func(m *model) {
			m.report.Incomplete, m.report.Planned = true, 20*time.Second
		}},
		{"failed", "run failed: connection lost", "rpc error", func(m *model) {
			m.err = errors.New("connection lost: rpc error: code = Unavailable desc = " + strings.Repeat("x", 200))
		}},
		{"failed, not ASCII", "run failed: ?????????? ????????", "rpc error", func(m *model) {
			m.err = errors.New("соединение потеряно: rpc error: code = Unavailable desc = " + strings.Repeat("x", 200))
		}},
	}
}

// Ground: boundary — when the full verdict leaves no room for one table row,
// the screen shows its short form and at least one row with numbers; the full
// text is in the report printed after exit.
func TestFinalScreenShortensTheVerdictToKeepARow(t *testing.T) {
	for _, tc := range verdictCases() {
		for _, width := range []int{60, 64} {
			t.Run(tc.name+"/"+strconv.Itoa(width), func(t *testing.T) {
				m := testModel(t)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 16})
				m.done, m.report = true, tableReport()
				tc.setup(m)

				view := m.View()
				if lines := strings.Count(view, "\n") + 1; lines > 16 {
					t.Errorf("the view is %d lines", lines)
				}
				// The body's words in reading order, the frame's borders and
				// the note's marks aside.
				flat := strings.Join(strings.Fields(strings.NewReplacer("│", " ", ">", " ").Replace(ansiCodes.ReplaceAllString(view, ""))), " ")
				short, full := strings.Contains(flat, tc.short), strings.Contains(flat, tc.fullOnly)
				if short == full {
					t.Errorf("want the verdict once, short %q or full: short %v, full %v:\n%s", tc.short, short, full, view)
				}
				if !strings.Contains(flat, "11ms 12ms 13ms 14ms") {
					t.Errorf("no table row with its numbers:\n%s", view)
				}
				if !strings.Contains(flat, "the full report is printed after exit") {
					t.Errorf("the screen does not say where the rest is:\n%s", view)
				}
				if short {
					checkCutCount(t, m, width, ansiCodes.ReplaceAllString(view, ""))
				}
			})
		}
	}
}

// Ground: contract — a short verdict is ASCII and takes at most two lines at
// the narrowest width, or it would not save the room it is for.
func TestShortVerdictsAreASCIIAndTwoLinesAt60(t *testing.T) {
	for _, tc := range verdictCases() {
		m := testModel(t)
		m.done, m.report = true, tableReport()
		tc.setup(m)
		for _, v := range m.shortVerdicts(contentWidth(minWidth)) {
			for _, r := range v {
				if r > 127 {
					t.Errorf("%s: %q is not ASCII", tc.name, v)
					break
				}
			}
			if n := len(strings.Split(wrapNote(v, contentWidth(minWidth)), "\n")); n > 2 {
				t.Errorf("%s: %q takes %d lines at 60 columns", tc.name, v, n)
			}
		}
	}
}

// checkCutCount checks "N more lines" against the full screen: every line of
// the report the view left out is counted, the full verdict its short form
// replaced among them.
func checkCutCount(t *testing.T, m *model, width int, view string) {
	t.Helper()

	full := 0
	for line := range strings.Lines(m.finalReport(contentWidth(width))) {
		if strings.TrimSpace(line) != "" {
			full++
		}
	}

	var body []string
	for line := range strings.Lines(view) {
		body = append(body, strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│")))
	}
	shown, counting, cut := 0, false, -1
	for _, line := range body {
		switch {
		case strings.HasPrefix(line, "Run finished"):
			counting = true
		case strings.Contains(line, "more method"):
			counting = false
		case strings.HasPrefix(line, "> ") && strings.Contains(line, "more lines"):
			counting = false
			cut = atoi(t, strings.Fields(line)[1])
		}
		if counting && line != "" && !strings.HasPrefix(line, "> invalid run:") &&
			!strings.HasPrefix(line, "> incomplete:") && !strings.HasPrefix(line, "> run failed:") {
			shown++
		}
	}
	if cut != full-shown {
		t.Errorf("the screen says %d more lines; the full screen has %d and %d are shown:\n%s", cut, full, shown, view)
	}
}
