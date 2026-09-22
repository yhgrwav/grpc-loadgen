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

// --- quitting -----------------------------------------------------------

// fakeView stands in for the tea program: it closes as soon as it runs, the
// way the view does when q is pressed at the first tick.
type fakeView struct{}

func (fakeView) Run() (tea.Model, error) { return nil, nil }
func (fakeView) Send(tea.Msg)            {}

func TestRunLiveWaitsForTheRunBeforeReturning(t *testing.T) {
	errStopped := errors.New("stopped")

	stop := make(chan struct{})
	finished := make(chan struct{})

	run := func() error {
		<-stop
		close(finished)

		return errStopped
	}

	done := make(chan error, 1)
	go func() { done <- RunLive(fakeView{}, run, func() { close(stop) }) }()

	select {
	case err := <-done:
		select {
		case <-finished:
		default:
			t.Fatal("RunLive returned while the run was still going: the report would be built from a live engine")
		}
		if !errors.Is(err, errStopped) {
			t.Errorf("err = %v, want the run's own error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunLive did not return: the view closed but the run was never cancelled")
	}
}

// --- layout hint --------------------------------------------------------

// ticksFor is how many refresh ticks cover d.
func ticksFor(d time.Duration) int {
	return int((d + refresh - 1) / refresh)
}

func tickN(m *model, n int) {
	for range n {
		m.Update(tickMsg(time.Now()))
	}
}

func TestUnknownKeyExplainsTheLayout(t *testing.T) {
	for _, key := range []string{"ж", "x"} {
		t.Run(key, func(t *testing.T) {
			m := testModel(t)
			press(m, key)

			if footer := m.footer(); !strings.Contains(footer, "«"+key+"»") {
				t.Errorf("footer = %q, want a hint naming %q", footer, key)
			}
		})
	}
}

func TestCommandsShowNoLayoutHint(t *testing.T) {
	m := testModel(t)
	press(m, "?")

	if footer := m.footer(); strings.Contains(footer, "«") {
		t.Errorf("footer = %q, want no hint after a real command", footer)
	}
}

func TestLayoutHintWorksWhileEditingSettings(t *testing.T) {
	m := testModel(t)
	m.active = m.settingsTab()
	m.editing = true

	press(m, "ж")

	if footer := m.footer(); !strings.Contains(footer, "«ж»") {
		t.Errorf("footer = %q, want the hint in the settings editor too", footer)
	}
}

func TestLayoutHintFadesAndGoes(t *testing.T) {
	m := testModel(t)
	press(m, "ж")

	tickN(m, ticksFor(1400*time.Millisecond))
	if stage := m.hintStage(); stage != 0 {
		t.Errorf("stage at 1.4s = %d, want 0: full brightness for the first 1.5s", stage)
	}

	tickN(m, ticksFor(300*time.Millisecond))
	if stage := m.hintStage(); stage <= 0 {
		t.Errorf("stage at 1.7s = %d, want a fading stage above 0", stage)
	}

	tickN(m, ticksFor(500*time.Millisecond))
	if footer := m.footer(); strings.Contains(footer, "«ж»") {
		t.Errorf("footer at 2.2s = %q, want the hint gone and the usual hints back", footer)
	}
}

func TestAnotherWrongKeyRestartsTheHint(t *testing.T) {
	m := testModel(t)

	press(m, "ж")
	tickN(m, ticksFor(1200*time.Millisecond))
	press(m, "x")
	tickN(m, ticksFor(1200*time.Millisecond))

	footer := m.footer()
	if !strings.Contains(footer, "«x»") || strings.Contains(footer, "«ж»") {
		t.Errorf("footer = %q, want only the latest key, still shown 1.2s after it", footer)
	}
}

// --- header and width ---------------------------------------------------

func TestNothingWrapsInsideTheFrame(t *testing.T) {
	// The frame does not let a line out past the terminal: it wraps it inside,
	// which is how "running" ended up alone on the next line. So the check is
	// on the body against the width the frame leaves, not on the whole view.
	for _, width := range []int{60, 80, 120, 200} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := testModel(t)
			m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
			tickN(m, 3)

			limit := contentWidth(width)

			check := func(screen string) {
				for i, line := range strings.Split(m.body(width), "\n") {
					if w := lipgloss.Width(line); w > limit {
						t.Errorf("%s, line %d is %d wide, the frame leaves %d: %q", screen, i, w, limit, line)
					}
				}
			}

			for tab := range m.tabs {
				m.active = tab
				check("tab " + strconv.Itoa(tab))
			}

			m.done = true
			m.active = 0
			m.report = engine.Report{Methods: []engine.MethodReport{{
				Method: "/wallet.v1.WalletService/GetBalanceWithAVeryLongName", Sent: 1_000_000, Failed: 12,
				P50: exact(31), P90: exact(35), P99: exact(36),
			}}}
			check("final report")
		})
	}
}

func TestFakeTargetIsNamedInTheInterfaceLanguage(t *testing.T) {
	m := testModel(t)
	m.target = FakeTarget
	m.settings.Lang = string(LangRU)
	m.applySettings()

	if header := m.header(74); !strings.Contains(header, "заглушка") {
		t.Errorf("header = %q, want the fake target named in Russian", header)
	}
}

// --- contrast -----------------------------------------------------------

// xterm returns the RGB of a 256-colour code, or ok=false for a code this
// test does not model.
func xterm(c lipgloss.Color) (rgb [3]float64, ok bool) {
	n, err := strconv.Atoi(string(c))
	if err != nil || n < 16 || n > 255 {
		return rgb, false
	}
	if n >= 232 {
		v := float64(8 + 10*(n-232))

		return [3]float64{v, v, v}, true
	}

	levels := []float64{0, 95, 135, 175, 215, 255}
	n -= 16

	return [3]float64{levels[n/36], levels[(n/6)%6], levels[n%6]}, true
}

// contrast is the WCAG 2 contrast ratio of two colours.
func contrast(a, b [3]float64) float64 {
	lum := func(c [3]float64) float64 {
		ch := func(v float64) float64 {
			v /= 255
			if v <= 0.03928 {
				return v / 12.92
			}

			return math.Pow((v+0.055)/1.055, 2.4)
		}

		return 0.2126*ch(c[0]) + 0.7152*ch(c[1]) + 0.0722*ch(c[2])
	}

	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

func TestTextColoursMeetTheContrastMinimum(t *testing.T) {
	// WCAG 2, success criterion 1.4.3: 4.5:1 for text.
	const minimum = 4.5

	for _, palette := range Palettes() {
		for _, mode := range []Mode{ModeDark, ModeLight} {
			theme := ThemeFor(palette.Name, mode)

			bg, ok := xterm(theme.Bg)
			if !ok {
				t.Fatalf("%s/%s: background %q is not a 256-colour code", palette.Name, mode, theme.Bg)
			}

			for name, c := range map[string]lipgloss.Color{
				"Text": theme.Text, "Muted": theme.Muted, "Accent": theme.Accent,
				"Good": theme.Good, "Warn": theme.Warn, "Bad": theme.Bad,
			} {
				fg, ok := xterm(c)
				if !ok {
					t.Errorf("%s/%s %s: %q is not a 256-colour code", palette.Name, mode, name, c)

					continue
				}
				if ratio := contrast(fg, bg); ratio < minimum {
					t.Errorf("%s/%s %s %s on %s: %.1f:1, want at least %.1f:1",
						palette.Name, mode, name, c, theme.Bg, ratio, minimum)
				}
			}
		}
	}
}

// --- latency rows -------------------------------------------------------

func exact(ms int) metrics.Quantile {
	return metrics.Quantile{Value: time.Duration(ms) * time.Millisecond, Exact: true, Defined: true}
}

// latencyRow returns the spark cells of the row for label, in order.
func latencyRow(t *testing.T, chart, label string) []rune {
	t.Helper()

	for line := range strings.Lines(chart) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != label {
			continue
		}

		var cells []rune
		for _, r := range line {
			if strings.ContainsRune("▁▂▃▄▅▆▇█·", r) {
				cells = append(cells, r)
			}
		}

		return cells
	}

	t.Fatalf("no %s row in:\n%s", label, chart)

	return nil
}

func TestLatencyShowsThreeLabelledRows(t *testing.T) {
	m := testModel(t)
	h := &history{}
	h.push(1, exact(10), exact(20), exact(30))

	chart := m.latencyChart(h.points)

	for _, label := range []string{"p50", "p90", "p99"} {
		latencyRow(t, chart, label)
	}
}

func TestLatencyRowsShareOneScale(t *testing.T) {
	// Flat series at 10, 20 and 30 ms. Scaled row by row, all three would sit
	// at the same height; on one scale p50 is at the bottom and p99 at the top.
	m := testModel(t)
	h := &history{}
	for range 5 {
		h.push(1, exact(10), exact(20), exact(30))
	}

	chart := m.latencyChart(h.points)
	p50, p99 := latencyRow(t, chart, "p50"), latencyRow(t, chart, "p99")

	if last := p50[len(p50)-1]; last != '▁' {
		t.Errorf("p50 cell = %q, want the lowest level", last)
	}
	if last := p99[len(p99)-1]; last != '█' {
		t.Errorf("p99 cell = %q, want the highest level", last)
	}
}

func TestLatencySameValueSameHeightInEveryRow(t *testing.T) {
	m := testModel(t)
	h := &history{}
	h.push(1, exact(10), exact(20), exact(30))
	h.push(1, exact(15), exact(15), exact(15))

	chart := m.latencyChart(h.points)

	last := func(label string) rune {
		row := latencyRow(t, chart, label)

		return row[len(row)-1]
	}

	if a, b, c := last("p50"), last("p90"), last("p99"); a != b || b != c {
		t.Errorf("15ms drawn as %q, %q, %q, want one height", a, b, c)
	}
}

func TestLatencyGapWhereNothingWasMeasured(t *testing.T) {
	m := testModel(t)
	h := &history{}
	h.push(1, exact(10), exact(20), exact(30))
	h.push(1, exact(10), exact(20), metrics.Quantile{})
	h.push(1, exact(10), exact(20), exact(30))

	row := latencyRow(t, m.latencyChart(h.points), "p99")
	if gap := row[len(row)-2]; gap != '·' {
		t.Errorf("p99 cell with no measurement = %q, want a gap", gap)
	}
}

func TestLatencyMarksALowerBound(t *testing.T) {
	m := testModel(t)
	h := &history{}
	h.push(1, exact(10), exact(20), metrics.Quantile{Value: time.Second, Defined: true})

	row := latencyLine(t, m.latencyChart(h.points), "p99")
	if !strings.Contains(row, ">") {
		t.Errorf("p99 row %q does not mark its value as a lower bound", row)
	}
}

func latencyLine(t *testing.T, chart, label string) string {
	t.Helper()

	for line := range strings.Lines(chart) {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == label {
			return line
		}
	}

	t.Fatalf("no %s row in:\n%s", label, chart)

	return ""
}

func TestLatencyWithNoHistoryStillHasItsRows(t *testing.T) {
	m := testModel(t)
	chart := m.latencyChart(nil)

	for _, label := range []string{"p50", "p90", "p99"} {
		latencyRow(t, chart, label)
	}
}

// --- supported layouts --------------------------------------------------

func TestRussianLayoutWalksTabs(t *testing.T) {
	// l and h sit under д and р.
	m := testModel(t)

	press(m, "д")
	if m.active != 1 {
		t.Fatalf("after д active = %d, want 1, as after l", m.active)
	}

	press(m, "р")
	if m.active != 0 {
		t.Errorf("after р active = %d, want 0, as after h", m.active)
	}
}

func TestRussianLayoutLeavesTheReport(t *testing.T) {
	m := testModel(t)
	m.done = true

	if _, cmd := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune("й")})); cmd == nil {
		t.Error("й on the report did not quit, as q does")
	}
}

func TestRussianLayoutLeavesTheSetupWizard(t *testing.T) {
	settings := &Settings{Mode: string(ModeDark), Palette: Palettes()[0].Name}

	m := &setupModel{settings: settings, text: NewText(LangEN)}
	m.restyle()

	if _, cmd := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune("й")})); cmd == nil {
		t.Error("й did not leave the setup wizard, as q does")
	}
}
