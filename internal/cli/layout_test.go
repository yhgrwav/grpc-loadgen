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
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

var allLangs = []Lang{LangRU, LangEN, LangDE, LangZH}

func TestCompactCount_UnitIsChosenAfterRounding(t *testing.T) {
	for _, tt := range []struct {
		n    uint64
		want string
	}{
		{0, "0"},
		{9_999, "9999"},
		{10_000, "10.0k"},
		{999_949, "999.9k"},
		{999_950, "1.0M"},
		{999_949_999, "999.9M"},
		{999_950_000, "1.0G"},
		{3_600_000_000, "3.6G"},         // 100k rps for 10 hours
		{3_153_600_000_000, "3153.6G"},  // 100k rps for a year
		{9_999_949_999_999, "9999.9G"},  // the widest form
		{9_999_950_000_000, ">9999.9G"}, // past it, a bound
		{math.MaxUint64, ">9999.9G"},
	} {
		if got := compactCount(tt.n); got != tt.want {
			t.Errorf("compactCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// widestCount is the widest a compact count gets.
const widestCount = 9_999_950_000_000

// widestBound is the widest percentile the report prints: a bound of hours.
var widestBound = metrics.Quantile{Value: 999*time.Minute + 59*time.Second, Defined: true}

type screenState struct {
	name  string
	setup func(m *model)
}

// liveStates cover every note and the widest value of every field: the
// scales must clamp, the history must cut, the stat lines must fit.
var liveStates = []screenState{
	{"normal", func(*model) {}},
	{"widest", widest},
	{"warmup note", func(m *model) {
		m.warmup = time.Hour
		m.snapshot.Elapsed = time.Second
	}},
	{"errors note", func(m *model) {
		m.snapshot.Sent, m.snapshot.Failed = 100, 100
	}},
	{"in-flight note", func(m *model) {
		m.snapshot.InFlight = 1_000_000
	}},
}

func widest(m *model) {
	s := &m.snapshot
	s.Sent, s.Failed = widestCount, widestCount
	s.InFlight, s.NotSent = widestCount, widestCount
	s.RPS = 9_999_999
	s.P50, s.P90, s.P99 = widestBound, widestBound, widestBound

	for i := range s.Methods {
		mm := &s.Methods[i]
		mm.Method = "/wallet.v1.WalletService/GetBalanceWithAVeryLongNameIndeed"
		mm.Sent, mm.Failed = widestCount, widestCount
		mm.RPS, mm.TargetRPS = 99_999_999, 9_999_999
		mm.P50, mm.P90, mm.P99 = widestBound, widestBound, widestBound
	}

	// A history longer than any row, with a spike ten times the usual latency.
	usual, spike := exact(30), exact(300)
	for i := range historyLimit + 10 {
		q := usual
		if i%7 == 0 {
			q = spike
		}

		m.overall.push(s.RPS, q, q, q)
		for _, h := range m.perMethod {
			h.push(s.RPS, q, q, q)
		}
	}
}

func widestReport() engine.Report {
	method := engine.MethodReport{
		Method: "/wallet.v1.WalletService/GetBalanceWithAVeryLongNameIndeed",
		Sent:   widestCount, Failed: widestCount, RPS: 99_999_999,
		P50: widestBound, P90: widestBound, P95: widestBound, P99: widestBound,
		Overload: engine.RefusalLatency{Count: widestCount,
			P50: widestBound, P90: widestBound, P95: widestBound, P99: widestBound},
	}

	return engine.Report{
		Sent: widestCount, Failed: widestCount, NotSent: widestCount, Duration: 999*time.Minute + 59*time.Second,
		Methods: []engine.MethodReport{method, method},
	}
}

func TestNothingWrapsInsideTheFrame(t *testing.T) {
	// The frame does not let a line out past the terminal: it wraps it inside,
	// which is how "running" ended up alone on the next line. So the check is
	// on the body against the width the frame leaves, not on the whole view.
	for _, lang := range allLangs {
		for width := minWidth; width <= 120; width++ {
			for _, state := range liveStates {
				t.Run(string(lang)+"/"+strconv.Itoa(width)+"/"+state.name, func(t *testing.T) {
					m := testModel(t)
					m.text = NewText(lang)
					m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
					tickN(m, 3)
					state.setup(m)

					for tab := range m.tabs {
						m.active = tab
						checkFits(t, m, width, "tab "+strconv.Itoa(tab))
					}

					m.active = m.settingsTab()
					m.editing = true
					checkFits(t, m, width, "settings, editing")
					m.editing = false

					m.showHelp = true
					checkFits(t, m, width, "help")
					m.showHelp = false

					m.done = true
					m.active = 0
					m.report = widestReport()
					checkFits(t, m, width, "final report")

					m.stopper.Press()
					checkFits(t, m, width, "final report, stopped")

					m.stopper = NewStopper(func() {}, func() {}, func() {}, time.Hour)
					m.err = errors.New("rpc error: code = Unavailable desc = connection refused to 10.0.0.1:50051 " +
						strings.Repeat("x", 80))
					checkFits(t, m, width, "final report, error")
				})
			}
		}
	}
}

func checkFits(t *testing.T, m *model, width int, screen string) {
	t.Helper()

	limit := contentWidth(width)
	for i, line := range strings.Split(m.body(width), "\n") {
		if w := lipgloss.Width(line); w > limit {
			t.Errorf("%s, line %d is %d wide, the frame leaves %d: %q", screen, i, w, limit, line)
		}
	}
}

// ambiguousInText are the characters a CJK terminal may draw two columns
// wide while lipgloss counts one. Text lines use ASCII instead; the frame,
// bars and spinner are graphics and stay.
const ambiguousInText = "›·—–≥…←→↑↓«»"

func TestTextLinesUseNoAmbiguousWidthCharacters(t *testing.T) {
	for _, lang := range allLangs {
		for _, state := range liveStates {
			t.Run(string(lang)+"/"+state.name, func(t *testing.T) {
				m := testModel(t)
				m.text = NewText(lang)
				m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				tickN(m, 3)
				state.setup(m)

				var screens []string
				for tab := range m.tabs {
					m.active = tab
					screens = append(screens, m.body(120))
				}
				m.showHelp = true
				screens = append(screens, m.body(120))
				m.showHelp = false
				press(m, "x")
				screens = append(screens, m.footer())
				m.active, m.editing = m.settingsTab(), true
				screens = append(screens, m.footer())
				m.active, m.editing = 0, false
				m.done, m.report = true, widestReport()
				screens = append(screens, m.body(120))
				m.stopper.Press()
				screens = append(screens, m.body(120))

				setup := &setupModel{settings: &Settings{Mode: string(ModeDark), Palette: Palettes()[0].Name}, text: NewText(lang)}
				setup.restyle()
				screens = append(screens, setup.View())

				for _, screen := range screens {
					if i := strings.IndexAny(screen, ambiguousInText); i >= 0 {
						line := screen[strings.LastIndex(screen[:i], "\n")+1:]
						line, _, _ = strings.Cut(line, "\n")
						t.Errorf("ambiguous-width character in a text line: %q", line)
					}
				}
			})
		}
	}
}

// statLineWith returns the body line holding label, or fails.
func statLineWith(t *testing.T, body, label string) string {
	t.Helper()

	for line := range strings.Lines(body) {
		if strings.Contains(line, label) {
			return strings.TrimRight(line, "\n")
		}
	}
	t.Fatalf("no line with %q in:\n%s", label, body)

	return ""
}

func TestStatLineNeverDropsInFlightOrErrors(t *testing.T) {
	for _, lang := range allLangs {
		t.Run(string(lang), func(t *testing.T) {
			m := testModel(t)
			m.text = NewText(lang)
			m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})
			tickN(m, 3)
			widest(m)

			line := statLineWith(t, m.body(minWidth), m.text.InFlight())
			if !strings.Contains(line, m.text.Errors()) {
				t.Errorf("errors dropped from %q", line)
			}
			if !strings.Contains(line, ">9999.9G") {
				t.Errorf("in flight not compact in %q", line)
			}
		})
	}
}

func TestStatLineCompactsBeforeItDrops(t *testing.T) {
	m := testModel(t)
	m.text = NewText(LangEN)
	const width = 62
	m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	tickN(m, 3)
	m.snapshot.Sent, m.snapshot.InFlight = 1_000_000, 1_000_000

	// The frame leaves 54. Exact, "sent 1 000 000  |  rps 0  |  in flight
	// 1 000 000  |  errors 0.0%" is 64; compact, with 1.0M twice, is 54 and
	// fits: nothing drops.
	line := statLineWith(t, m.body(width), m.text.InFlight())
	for _, want := range []string{"sent 1.0M", "rps", "in flight 1.0M", "errors"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing from %q", want, line)
		}
	}
}

func TestStatLineDropsSentFirstThenRate(t *testing.T) {
	m := testModel(t)
	m.text = NewText(LangEN)
	m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})
	tickN(m, 3)

	// The frame leaves 52. Compact, "sent 1.0M  |  rps 0  |  in flight 1.0M
	// |  errors 100.0%" is 56: exactly one field must go, and it is sent.
	m.snapshot.Sent, m.snapshot.Failed, m.snapshot.InFlight = 1_000_000, 1_000_000, 1_000_000

	line := statLineWith(t, m.body(minWidth), m.text.InFlight())
	if strings.Contains(line, m.text.Sent()) {
		t.Errorf("sent kept on a line that had to drop one field: %q", line)
	}
	if !strings.Contains(line, "rps") {
		t.Errorf("rate dropped while dropping sent alone was enough: %q", line)
	}
}

func TestStatLineIsExactWhenItFits(t *testing.T) {
	m := testModel(t)
	m.text = NewText(LangEN)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tickN(m, 3)
	m.snapshot.InFlight = 1_000_000

	if line := statLineWith(t, m.body(120), m.text.InFlight()); !strings.Contains(line, "1 000 000") {
		t.Errorf("want the exact count where it fits: %q", line)
	}
}

func TestNoteFallsBackToTheShortForm(t *testing.T) {
	for _, lang := range allLangs {
		t.Run(string(lang), func(t *testing.T) {
			m := testModel(t)
			m.text = NewText(lang)
			tickN(m, 3)
			m.snapshot.InFlight = 1_000_000

			m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			if body := m.body(120); !strings.Contains(body, m.text.InFlightNote()) {
				t.Errorf("width 120: want the full note")
			}

			m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})
			body := m.body(minWidth)
			full := lipgloss.Width("> "+m.text.InFlightNote()) <= contentWidth(minWidth)
			if full {
				return
			}
			if strings.Contains(body, m.text.InFlightNote()) {
				t.Errorf("width %d: the full note does not fit, yet it is shown", minWidth)
			}
			if want := m.text.InFlightNoteShort(); want == "" || !strings.Contains(body, "> "+want) {
				t.Errorf("width %d: want the short note %q", minWidth, want)
			}
		})
	}
}

func TestShortNotesFitTheNarrowestFrame(t *testing.T) {
	room := contentWidth(minWidth) - lipgloss.Width("> ")

	for _, lang := range allLangs {
		text := NewText(lang)
		for name, note := range map[string]string{
			"warmup":    text.WarmupNoteShort(9999999), // seven digits: 100k rps for 100s
			"errors":    text.ErrorsNoteShort(),
			"in flight": text.InFlightNoteShort(),
		} {
			if w := lipgloss.Width(note); w > room || note == "" {
				t.Errorf("%s %s: %q is %d wide, room %d", lang, name, note, w, room)
			}
		}
	}
}

func TestFinalTableColumnsLineUp(t *testing.T) {
	for _, lang := range allLangs {
		for _, width := range []int{minWidth, 80, 120} {
			t.Run(string(lang)+"/"+strconv.Itoa(width), func(t *testing.T) {
				m := testModel(t)
				m.text = NewText(lang)
				m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
				m.done, m.report = true, widestReport()

				var lines []string
				inTable := false
				for line := range strings.Lines(m.finalReport(contentWidth(width))) {
					line = strings.TrimRight(line, "\n")
					if strings.HasPrefix(line, m.text.ColumnMethod()) {
						inTable = true
					}
					if inTable && line == "" {
						break
					}
					if inTable {
						lines = append(lines, line)
					}
				}
				if len(lines) < 2 {
					t.Fatalf("table not found")
				}

				// One line per row, or a name line followed by the lines of
				// numbers; each line of numbers lines up with its heading.
				period := 1
				if strings.TrimSpace(lines[0]) == m.text.ColumnMethod() {
					for period < len(lines) && strings.Contains(lines[period], "p99") ||
						period < len(lines) && strings.Contains(lines[period], "sent/s") {
						period++
					}
				}
				for i, line := range lines {
					kind := i % period
					if period > 1 && kind == 0 {
						continue
					}
					if w, want := lipgloss.Width(line), lipgloss.Width(lines[kind]); w != want {
						t.Errorf("line %d is %d wide, its heading %d: columns do not line up\n%s",
							i, w, want, strings.Join(lines, "\n"))
					}
				}
			})
		}
	}
}

// --- below the minimum width ---------------------------------------------

func TestNarrowTerminalAsksToWiden(t *testing.T) {
	for _, width := range []int{minWidth - 1, 20, 1} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := testModel(t)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 40})

			view := m.View()
			if strings.Contains(view, "\n") {
				t.Errorf("want one line, got %q", view)
			}
			if w := lipgloss.Width(view); w > width {
				t.Errorf("message is %d wide on a terminal of %d: %q", w, width, view)
			}
			if width >= 30 && !strings.Contains(view, strconv.Itoa(minWidth)) {
				t.Errorf("message does not say how wide: %q", view)
			}
		})
	}
}

func TestUnknownWidthKeepsTheFrame(t *testing.T) {
	m := testModel(t)

	if view := m.View(); !strings.Contains(view, "╭") {
		t.Errorf("before the first size message the frame must be drawn, got %q", view)
	}
}

func TestNarrowTerminalKeepsTheKeys(t *testing.T) {
	c := newStopCalls()
	m := testModel(t)
	m.stopper = c.stopper(time.Hour)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 40})

	q := tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune("q")})
	m.Update(q)
	m.Update(q)
	m.Update(q)

	if c.stop.Load() != 1 || c.abort.Load() != 1 || c.exit.Load() != 1 {
		t.Errorf("stop/abort/exit = %d/%d/%d, want 1/1/1: the three presses must work below the minimum width",
			c.stop.Load(), c.abort.Load(), c.exit.Load())
	}
}

func TestNarrowTerminalKeepsTheRunGoing(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 40})

	before := len(m.overall.rps)
	tickN(m, 2)

	if got := len(m.overall.rps); got != before+2 {
		t.Errorf("history %d after 2 ticks from %d: the live view stopped while narrow", got, before)
	}
}

func TestWideningBringsTheFrameBack(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 40})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	if view := m.View(); !strings.Contains(view, "╭") {
		t.Errorf("after widening to 100 the frame is not back: %q", firstLine(view))
	}
}

// The frame test covers the upper clamp and the cut history; these two edges
// it never feeds: a value below zero, and no history at all.
func TestGauge_NegativeValueDrawsAnEmptyBar(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	// -50 of 100 over 10 cells is -5 cells, which strings.Repeat panics on.
	if got := lipglossWidth(gauge(s, -50, 100, 10)); got != 10 {
		t.Errorf("gauge width with a negative value = %d, want 10", got)
	}
}

func TestSparkline_NoHistoryStillFillsTheRow(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	if got := lipglossWidth(sparkline(s, nil, 20)); got != 20 {
		t.Errorf("empty sparkline width = %d, want 20", got)
	}
}

func TestFooterKeepsTheWayOutAtTheNarrowestFrame(t *testing.T) {
	for _, lang := range allLangs {
		t.Run(string(lang), func(t *testing.T) {
			m := testModel(t)
			m.text = NewText(lang)
			m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})

			quit := m.text.HintQuit()
			if f := m.footer(); !strings.Contains(f, quit) {
				t.Errorf("live view footer lost %q: %q", quit, f)
			}

			m.active = m.settingsTab()
			for _, editing := range []bool{false, true} {
				m.editing = editing
				f := m.footer()
				if !strings.Contains(f, quit) || !strings.Contains(f, "esc") {
					t.Errorf("settings footer (editing %v) lost q or esc: %q", editing, f)
				}
			}
		})
	}
}

func TestFooterDropsHintsByOrderNotPosition(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))
	hints := []hint{{"aaaa", 2}, {"bbbb", 0}, {"cccc", 1}, {"q quit", 0}}

	// All four: 4+3+4+3+4+3+6 = 27. Without cccc: 20. Without both: 13.
	for _, tt := range []struct {
		width int
		want  string
	}{
		{27, "aaaa   bbbb   cccc   q quit"},
		{26, "aaaa   bbbb   q quit"},
		{19, "bbbb   q quit"},
		{5, "bbbb   q quit"}, // what never goes stays, even too wide
	} {
		if got := fitKeyHints(s, tt.width, hints...); got != tt.want {
			t.Errorf("width %d: %q, want %q", tt.width, got, tt.want)
		}
	}
}

func TestNegativeCountIsShownAsItIs(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tickN(m, 3)
	m.snapshot.InFlight = -3

	if line := statLineWith(t, m.body(120), m.text.InFlight()); !strings.Contains(line, "-3") {
		t.Errorf("a negative count is a bug and must show: %q", line)
	}
}

func TestWrapKeepsEveryCharacterOfALongWord(t *testing.T) {
	token := strings.Repeat("abcdefghij", 20)
	lines := wrapText("token "+token+" end", 52)

	// Spaces between words may land at a line break; every other character
	// must arrive, in order.
	if got := strings.ReplaceAll(strings.Join(lines, ""), " ", ""); got != "token"+token+"end" {
		t.Errorf("characters lost or added: %q", got)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 52 {
			t.Errorf("line %d is %d wide: %q", i, w, line)
		}
	}
}
