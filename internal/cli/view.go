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
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

// NewProgram builds the full-screen view of a run. RunLive drives it together
// with the run.
func NewProgram(target, service string, eng *engine.Engine, warmup time.Duration, settings *Settings, cancel func()) *tea.Program {
	m := newModel(target, eng, warmup, settings, cancel)
	m.service = service

	return tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
	)
}

func (m *model) View() string {
	width := m.viewWidth()

	frame := m.styles.frame.Width(width - 4)
	if m.height > 6 {
		frame = frame.Height(m.height - 4)
	}

	return frame.Render(m.body(width))
}

func (m *model) viewWidth() int {
	if m.width < 40 {
		return 72
	}

	return m.width
}

// contentWidth is what the frame leaves for the body on a terminal this wide:
// the border takes one column a side and the padding two.
func contentWidth(width int) int {
	return width - 4 - 4
}

// body is everything inside the frame. The frame wraps whatever is wider than
// its content width, so a line laid out too wide breaks in the middle.
func (m *model) body(width int) string {
	inner := contentWidth(width)

	var b strings.Builder

	b.WriteString(m.header(inner))
	b.WriteString("\n\n")
	b.WriteString(m.tabBar(inner))
	b.WriteString("\n\n")

	switch {
	case m.done:
		b.WriteString(m.finalReport(inner))
	case m.showHelp:
		b.WriteString(m.help())
	case m.active == 0:
		b.WriteString(m.summary(inner))
	case m.active == m.settingsTab():
		b.WriteString(m.settingsView(inner))
	default:
		b.WriteString(m.method(inner, m.active-1))
	}

	b.WriteString("\n\n")
	b.WriteString(m.footer())

	return b.String()
}

func (m *model) header(width int) string {
	status := m.text.Running()
	glyph := spinner(m.frame / 2)

	switch {
	case m.stopping:
		status = m.text.Stopping()
	case m.done:
		status = m.text.Finished()
		glyph = "●"
	}

	target := m.target
	if target == FakeTarget {
		target = m.text.FakeTarget()
	}

	// One line, read left to right: what is happening, to which service, at
	// which address. The project name lives in the footer; the header is about
	// the run.
	sep := m.styles.faint.Render("  ·  ")
	lead := m.styles.shimmer(glyph+" "+status, m.frame) + sep
	if m.service != "" {
		lead += m.styles.value.Render(m.service) + sep
	}
	target = truncate(target, width-lipgloss.Width(lead))

	s := m.snapshot
	clock := formatDuration(s.Elapsed) + " / " + formatDuration(s.Total)

	return lead + m.styles.muted.Render(target) + "\n" +
		progress(m.styles, s.Elapsed, s.Total, width-lipgloss.Width(clock)-1) + m.styles.pad(1) +
		m.styles.muted.Render(clock)
}

// truncate shortens s to width columns, marking the cut with an ellipsis.
func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width < 1 {
		return ""
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + "…"
}

func (m *model) tabBar(width int) string {
	var parts []string

	for i, name := range m.tabs {
		if i == m.active {
			parts = append(parts, m.styles.tabOn.Render(name))

			continue
		}
		parts = append(parts, m.styles.tab.Render(name))
	}

	row := strings.Join(parts, "")

	return row + "\n" + m.styles.faint.Render(strings.Repeat("─", width))
}

func (m *model) summary(width int) string {
	s := m.snapshot

	var b strings.Builder

	b.WriteString(statLine(m.styles,
		[2]string{m.text.Sent(), formatCount(s.Sent)},
		[2]string{"rps", fmt.Sprintf("%.0f", s.RPS)},
		[2]string{m.text.InFlight(), formatCount(s.InFlight)},
		[2]string{m.text.Errors(), m.errorShare(s.Sent, s.Failed)},
	))
	b.WriteString("\n")
	b.WriteString(statLine(m.styles,
		[2]string{"p50", formatQuantile(s.P50)},
		[2]string{"p90", formatQuantile(s.P90)},
		[2]string{"p99", formatQuantile(s.P99)},
	))
	b.WriteString("\n\n")

	b.WriteString(m.gaugeRow("rps", s.RPS, m.totalTarget(), fmt.Sprintf("%.0f", s.RPS)))
	b.WriteString("\n")
	b.WriteString(m.gaugeRow(m.text.InFlight(), float64(s.InFlight), float64(max(s.InFlight, 1)*2), formatCount(s.InFlight)))
	b.WriteString("\n\n")

	b.WriteString(m.sparkRow(m.text.Rate(), m.overall.rps, "", func(v float64) string {
		return fmt.Sprintf("%.0f", v)
	}))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(m.latencyChart(m.overall.points))

	if note := m.note(); note != "" {
		b.WriteString("\n\n")
		b.WriteString(m.styles.note.Render("› " + note))
	}

	return b.String()
}

func (m *model) method(width, index int) string {
	if index >= len(m.snapshot.Methods) {
		return m.styles.muted.Render("…")
	}

	method := m.snapshot.Methods[index]

	var b strings.Builder

	b.WriteString(m.styles.value.Render(displayMethod(method.Method)))
	b.WriteString("\n\n")

	b.WriteString(statLine(m.styles,
		[2]string{m.text.Sent(), formatCount(method.Sent)},
		[2]string{"rps", fmt.Sprintf("%.0f", method.RPS)},
		[2]string{m.text.Errors(), m.errorShare(method.Sent, method.Failed)},
	))
	b.WriteString("\n")
	b.WriteString(statLine(m.styles,
		[2]string{"p50", formatQuantile(method.P50)},
		[2]string{"p90", formatQuantile(method.P90)},
		[2]string{"p99", formatQuantile(method.P99)},
	))
	b.WriteString("\n\n")

	b.WriteString(m.gaugeRow("rps", method.RPS, float64(method.TargetRPS), fmt.Sprintf("%.0f / %d", method.RPS, method.TargetRPS)))
	b.WriteString("\n\n")

	h := m.perMethod[method.Method]
	if h == nil {
		h = &history{}
	}

	b.WriteString(m.sparkRow(m.text.Rate(), h.rps, "", func(v float64) string {
		return fmt.Sprintf("%.0f", v)
	}))
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(m.latencyChart(h.points))

	return b.String()
}

// latencyChart is three sparkline rows, p50 to p99, on one shared scale: the
// height of a cell means the same value in every row, so the gap between p50
// and p99 is visible at a glance. The scale is written once, above the rows.
func (m *model) latencyChart(points []point) string {
	series := latencySeriesOf(points)
	low, high, ok := latencyScale(series)
	width := m.sparkCells()

	scale := "-"
	if ok {
		scale = formatDuration(millis(low)) + " – " + formatDuration(millis(high))
	}

	var b strings.Builder

	b.WriteString(m.styles.label.Render(fmt.Sprintf("%-10s", m.text.Latency())))
	b.WriteString(m.styles.muted.Render(scale))

	for _, s := range series {
		b.WriteString("\n")
		b.WriteString(m.styles.label.Render(fmt.Sprintf("  %-8s", s.label)))
		b.WriteString(latencyCells(m.styles, s.values, s.bounds, low, high, width))
		b.WriteString(m.styles.pad(2))
		b.WriteString(m.styles.value.Render(latencyValue(s.values, s.bounds)))
	}

	return b.String()
}

// Columns a spark row keeps beside its cells: the label and the value after.
const (
	sparkLabelWidth = 10
	sparkValueWidth = 18
)

// sparkCells is how many cells a spark row gets: as many as fit beside its
// label and value, up to the length of the history, and never so few the line
// means nothing.
func (m *model) sparkCells() int {
	room := contentWidth(m.viewWidth()) - sparkLabelWidth - 2 - sparkValueWidth

	return min(max(room, 8), historyLimit)
}

func (m *model) sparkRow(label string, values []float64, unit string, format func(float64) string) string {
	return m.styles.label.Render(fmt.Sprintf("%-10s", label)) +
		sparkline(m.styles, values, m.sparkCells()) +
		m.styles.pad(2) +
		sparkRange(m.styles, values, unit, format)
}

func (m *model) gaugeRow(label string, value, limit float64, text string) string {
	return m.styles.label.Render(fmt.Sprintf("%-10s", label)) +
		gauge(m.styles, value, limit, min(max(gaugeWidth, m.sparkCells()/2), m.sparkCells())) + m.styles.pad(2) +
		m.styles.value.Render(text)
}

func (m *model) help() string {
	rows := [][2]string{
		{"←→ tab", m.text.HelpTabs()},
		{"enter", m.text.HelpSettings()},
		{"esc", m.text.HelpEscape()},
		{"?", m.text.HelpHelp()},
		{"q", m.text.HelpQuit()},
	}

	var b strings.Builder

	b.WriteString(m.styles.title.Render(m.text.HelpTitle()))
	b.WriteString("\n\n")

	for _, row := range rows {
		b.WriteString(m.styles.helpKey.Render(fmt.Sprintf("%-8s", row[0])))
		b.WriteString(m.styles.helpText.Render(row[1]))
		b.WriteString("\n")
	}

	return b.String()
}

func (m *model) footerHints() string {
	if stage := m.hintStage(); stage >= 0 {
		// Fades by stepping down the text colours: a terminal has no opacity.
		fade := []lipgloss.Style{m.styles.value, m.styles.muted, m.styles.faint}

		return fade[stage].Render(m.text.UnknownKey(m.hintKey))
	}

	if m.done {
		return keyHint(m.styles, m.text.HintTabs(), m.text.PressToExit())
	}

	if m.notice != "" {
		return m.styles.note.Render(m.notice)
	}

	if m.active == m.settingsTab() {
		if !m.editing {
			return m.styles.muted.Render(m.text.SettingsLocked())
		}

		return m.styles.muted.Render(m.text.SettingsHint())
	}

	if m.showHelp {
		return keyHint(m.styles, m.text.HintBack(), m.text.HintQuit())
	}

	return keyHint(m.styles, m.text.HintTabs(), m.text.HintHelp(), m.text.HintQuit())
}

func (m *model) settingsView(width int) string {
	rows := []struct {
		label   string
		options []string
	}{
		{label: m.text.LanguageRow(), options: m.langOptions()},
		{label: m.text.ModeRow(), options: m.modeOptions()},
		{label: m.text.PaletteRow(), options: m.paletteOptions()},
	}

	var b strings.Builder

	for i, row := range rows {
		marker := "   "
		if m.editing && settingsRow(i) == m.row {
			marker = " ▸ "
		}

		lead := m.styles.pickOn.Render(marker) + m.styles.label.Render(fmt.Sprintf("%-12s", row.label))
		b.WriteString(lead)
		b.WriteString(m.flow(row.options, lipgloss.Width(lead), width))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.styles.muted.Render(truncateLeft(m.settings.Path(), width)))

	return b.String()
}

// flow lays options out after a lead of indent columns, starting a new line,
// indented the same, where the next option would not fit in width.
func (m *model) flow(options []string, indent, width int) string {
	var b strings.Builder

	used := indent

	for i, option := range options {
		w := lipgloss.Width(option)

		if i > 0 {
			if used+2+w > width {
				b.WriteString("\n" + m.styles.pad(indent))
				used = indent
			} else {
				b.WriteString(m.styles.pad(2))
				used += 2
			}
		}

		b.WriteString(option)
		used += w
	}

	return b.String()
}

// truncateLeft keeps the end of s, where a path keeps its file name.
func truncateLeft(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width < 1 {
		return ""
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[1:]
	}

	return "…" + string(runes)
}

func (m *model) option(text string, selected bool) string {
	if selected {
		return m.styles.pickOn.Render("[") + text + m.styles.pickOn.Render("]")
	}

	return m.styles.pad(1) + text + m.styles.pad(1)
}

func (m *model) optionText(text string, selected bool) string {
	if selected {
		return m.styles.pickOn.Render(text)
	}

	return m.styles.pick.Render(text)
}

func (m *model) langOptions() []string {
	options := make([]string, 0, len(Languages()))

	for _, lang := range Languages() {
		selected := string(lang.Lang) == m.settings.Lang
		options = append(options, m.option(m.optionText(lang.Title, selected), selected))
	}

	return options
}

func (m *model) modeOptions() []string {
	light := Mode(m.settings.Mode) == ModeLight

	return []string{
		m.option(m.optionText(m.text.ModeDark(), !light), !light),
		m.option(m.optionText(m.text.ModeLight(), light), light),
	}
}

func (m *model) paletteOptions() []string {
	palettes := Palettes()
	options := make([]string, 0, len(palettes))

	for i := range palettes {
		theme := palettes[i].Dark
		if Mode(m.settings.Mode) == ModeLight {
			theme = palettes[i].Light
		}

		selected := palettes[i].Name == m.settings.Palette
		label := m.optionText(palettes[i].Name+" ", selected) + swatch(theme)
		options = append(options, m.option(label, selected))
	}

	return options
}

func (m *model) finalReport(width int) string {
	report := m.report

	title := m.text.ReportTitle()
	if m.stopping {
		title = m.text.ReportStopped()
	}

	var b strings.Builder

	b.WriteString(m.styles.title.Render(title))
	b.WriteString("\n")
	b.WriteString(statLine(m.styles,
		[2]string{m.text.Sent(), formatCount(report.Sent)},
		[2]string{m.text.Errors(), m.errorShare(report.Sent, report.Failed)},
		[2]string{"rps", fmt.Sprintf("%.0f", float64(report.Sent)/max(report.Duration.Seconds(), 1))},
		[2]string{m.text.Latency(), formatDuration(report.Duration)},
	))
	b.WriteString("\n" + "\n")

	// Sent, errors and p99 always fit; p50 and p90 go first when the terminal
	// is narrow, and the method name takes whatever is left.
	const (
		coreColumns = 1 + 9 + 1 + 8 + 1 + 9
		midColumns  = 1 + 9 + 1 + 9
		minName     = 12
	)

	wide := width-coreColumns-midColumns >= minName

	nameWidth := width - coreColumns
	if wide {
		nameWidth -= midColumns
	}
	nameWidth = min(max(nameWidth, minName), 40)

	header := fmt.Sprintf("%-*s %9s %8s", nameWidth, m.text.ColumnMethod(), m.text.Sent(), m.text.Errors())
	if wide {
		header += fmt.Sprintf(" %9s %9s", "p50", "p90")
	}
	header += fmt.Sprintf(" %9s", "p99")

	b.WriteString(m.styles.label.Render(header))
	b.WriteString("\n")

	for i := range report.Methods {
		method := &report.Methods[i]
		name := truncate(shortMethod(method.Method), nameWidth)

		errors := m.styles.value
		if method.Failed > 0 {
			errors = m.styles.bad
		}

		b.WriteString(m.styles.value.Render(fmt.Sprintf("%-*s", nameWidth, name)))
		b.WriteString(m.styles.value.Render(fmt.Sprintf(" %9s", formatCount(method.Sent))))
		b.WriteString(errors.Render(fmt.Sprintf(" %8s", m.errorShare(method.Sent, method.Failed))))
		if wide {
			b.WriteString(m.styles.muted.Render(fmt.Sprintf(" %9s %9s", formatQuantile(method.P50), formatQuantile(method.P90))))
		}
		b.WriteString(m.styles.value.Render(fmt.Sprintf(" %9s", formatQuantile(method.P99))))
		b.WriteString("\n")
	}

	if m.stopping {
		b.WriteString("\n")
		b.WriteString(m.styles.note.Render("› " + m.text.ReportStoppedNote()))
	}

	if m.err != nil && !m.stopping {
		b.WriteString("\n")
		b.WriteString(m.styles.bad.Render("› " + m.err.Error()))
	}

	return b.String()
}

func (m *model) note() string {
	s := m.snapshot

	if m.warmup > 0 && s.Elapsed < m.warmup {
		return m.text.WarmupNote(formatDuration(m.warmup - s.Elapsed))
	}
	if s.Sent > 0 && float64(s.Failed)/float64(s.Sent) > 0.05 {
		return m.text.ErrorsNote()
	}
	if s.InFlight > 0 && float64(s.InFlight) > m.totalTarget() {
		return m.text.InFlightNote()
	}

	return ""
}

func (m *model) errorShare(sent, failed int) string {
	if sent == 0 {
		return "0%"
	}

	return fmt.Sprintf("%.1f%%", float64(failed)/float64(sent)*100)
}

func (m *model) totalTarget() float64 {
	var total float64

	for _, method := range m.snapshot.Methods {
		total += float64(method.TargetRPS)
	}

	if total == 0 {
		return 1
	}

	return total
}

// FakeTarget is the target name the CLI passes when -fake is on.
const FakeTarget = "fake target"

// LiveView is the part of a tea program RunLive drives.
type LiveView interface {
	Run() (tea.Model, error)
	Send(msg tea.Msg)
}

// RunLive runs the view and the load side by side and returns the run's error.
// It returns only after the run has: the view can close first, on q, and a
// report built while requests are still being recorded would be a snapshot of
// a moving engine.
func RunLive(view LiveView, run func() error, cancel func()) error {
	finished := make(chan error, 1)

	go func() {
		err := run()
		finished <- err
		view.Send(doneMsg{err: err})
	}()

	_, viewErr := view.Run()

	cancel()
	runErr := <-finished

	if viewErr != nil {
		return viewErr
	}

	return runErr
}

// footer is the key hints with the project name at the right edge, where it
// signs the view without taking the header from the run. It is left out when
// the hints need the room.
func (m *model) footer() string {
	hints := m.footerHints()
	name := m.styles.faint.Render("grpc-loadgen")

	gap := contentWidth(m.viewWidth()) - lipgloss.Width(hints) - lipgloss.Width(name)
	if gap < 2 {
		return hints
	}

	return hints + m.styles.pad(gap) + name
}
