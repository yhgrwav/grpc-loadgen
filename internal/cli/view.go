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

	"github.com/yhgrwav/leettest/pkg/engine"
)

// NewProgram builds the full-screen view of a run. RunLive drives it together
// with the run.
func NewProgram(target, service string, eng *engine.Engine, warmup time.Duration, settings *Settings, stopper *Stopper) *tea.Program {
	m := newModel(target, eng, warmup, settings, stopper)
	m.service = service

	return tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
	)
}

// minWidth is the narrowest terminal the frame is laid out for. Below it
// the view is one line asking for more room; keys and the run go on.
const minWidth = 60

func (m *model) View() string {
	if m.width > 0 && m.width < minWidth {
		return truncate(m.text.TooNarrow(minWidth), m.width)
	}

	width := m.viewWidth()

	frame := m.styles.frame.Width(width - 4)
	if m.height > 6 {
		frame = frame.Height(m.height - 4)
	}

	return frame.Render(m.body(width))
}

func (m *model) viewWidth() int {
	if m.width < minWidth {
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
	case m.stopper.Stopping():
		status = m.text.Stopping()
	case m.done:
		status = m.text.Finished()
		glyph = "●"
	}

	target := m.target
	if target == FakeTarget {
		target = m.text.FakeTarget()
	}

	// One line, read left to right: what is happening, what a stop key would
	// do next, to which service, at which address. The project name lives in
	// the footer; the header is about the run.
	line := newHeaderLine(m.styles, width, m.styles.shimmer(glyph+" "+status, m.frame))
	line.add(m.styles.warn, m.stopHint(), m.stopAction())
	line.add(m.styles.value, m.service)
	line.add(m.styles.muted, target)

	s := m.snapshot
	clock := formatDuration(s.Elapsed) + " / " + formatDuration(s.Total)

	return line.text + "\n" +
		progress(m.styles, s.Elapsed, s.Total, width-lipgloss.Width(clock)-1) + m.styles.pad(1) +
		m.styles.muted.Render(clock)
}

// stopAction says what another press of the stop key does; it is empty while
// no stop is under way.
func (m *model) stopAction() string {
	if m.done {
		return ""
	}

	switch m.stopper.Stage() {
	case StageNone:
		return ""

	case StageStop:
		// With nothing in flight the run closes on its own, and another press
		// would have nothing to cut off.
		if m.snapshot.InFlight <= 0 {
			return ""
		}

		return m.text.StopAgainAborts()

	default:
		return m.text.StopAgainExits()
	}
}

// stopHint is the action with the count of calls the drain is still waiting
// for. Past the gentle stop the count is dropped: those calls are being cut
// off while the line is read, and the room goes to the way out of an abort
// that hangs.
func (m *model) stopHint() string {
	action := m.stopAction()
	if action == "" || m.stopper.Stage() > StageStop {
		return action
	}

	return m.text.InFlightCount(formatCount(m.snapshot.InFlight)) + " | " + action
}

// headerLine lays the header pieces out left to right, each behind a
// separator, and stops where the width runs out: the frame wraps a longer
// line, and a wrapped header shifts everything below it.
type headerLine struct {
	text string
	sep  string
	room int
}

// minPiece is the narrowest a cut piece may get: two columns and the
// ellipsis. Below it the piece carries nothing and is dropped.
const minPiece = 5

func newHeaderLine(s styles, width int, lead string) *headerLine {
	return &headerLine{
		text: lead,
		sep:  s.faint.Render("  |  "),
		room: width - lipgloss.Width(lead),
	}
}

// add appends the first piece that fits whole, falling back to the shorter
// ones given after it; the last one is cut to what is left. A piece with no
// room at all is dropped, the ones added before it keep theirs.
func (l *headerLine) add(style lipgloss.Style, texts ...string) {
	sepWidth := lipgloss.Width(l.sep)
	room := l.room - sepWidth

	for i, text := range texts {
		if text == "" {
			continue
		}

		if lipgloss.Width(text) > room {
			if i < len(texts)-1 {
				continue
			}
			// A shard of a word says nothing: below that the piece is
			// dropped and the line ends on the one before it.
			if room < minPiece {
				return
			}

			text = truncate(text, room)
		}

		l.text += l.sep + style.Render(text)
		l.room -= sepWidth + lipgloss.Width(text)

		return
	}
}

// ellipsis marks a cut. Three dots rather than "…": a CJK terminal may draw
// that one two columns wide while it is counted as one.
const ellipsis = "..."

// truncate shortens s to width columns, marking the cut with an ellipsis
// where there is room for one.
func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width < 1 {
		return ""
	}

	mark := ellipsis
	if width <= len(ellipsis) {
		mark = ""
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+len(mark) > width {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + mark
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

	b.WriteString(fitStatLine(m.styles, width,
		countField(m.text.Sent(), s.Sent, 1),
		statField{label: "rps", value: fmt.Sprintf("%.0f", s.RPS), drop: 2},
		countField(m.text.InFlight(), s.InFlight, 0),
		statField{label: m.text.Errors(), value: m.errorShare(s.Sent, s.Failed)},
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

	if full, short := m.note(); full != "" {
		note := "> " + full
		if lipgloss.Width(note) > width {
			note = "> " + short
		}
		b.WriteString("\n\n")
		b.WriteString(m.styles.note.Render(note))
	}

	return b.String()
}

func (m *model) method(width, index int) string {
	if index >= len(m.snapshot.Methods) {
		return m.styles.muted.Render(ellipsis)
	}

	method := m.snapshot.Methods[index]

	var b strings.Builder

	b.WriteString(m.styles.value.Render(displayMethod(method.Method)))
	b.WriteString("\n\n")

	b.WriteString(fitStatLine(m.styles, width,
		countField(m.text.Sent(), method.Sent, 1),
		statField{label: "rps", value: fmt.Sprintf("%.0f", method.RPS), drop: 2},
		statField{label: m.text.Errors(), value: m.errorShare(method.Sent, method.Failed)},
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

	b.WriteString(m.styles.label.Render(padRight(m.text.Latency(), sparkLabelWidth)))
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
	return m.styles.label.Render(padRight(label, sparkLabelWidth)) +
		sparkline(m.styles, values, m.sparkCells()) +
		m.styles.pad(2) +
		sparkRange(m.styles, values, unit, format)
}

func (m *model) gaugeRow(label string, value, limit float64, text string) string {
	return m.styles.label.Render(padRight(label, sparkLabelWidth)) +
		gauge(m.styles, value, limit, min(max(gaugeWidth, m.sparkCells()/2), m.sparkCells())) + m.styles.pad(2) +
		m.styles.value.Render(text)
}

func (m *model) help() string {
	rows := [][2]string{
		{"<- -> tab", m.text.HelpTabs()},
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

	mark := ellipsis
	if width <= len(ellipsis) {
		mark = ""
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+len(mark) > width {
		runes = runes[1:]
	}

	return mark + string(runes)
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
	if m.stopper.Stopping() {
		title = m.text.ReportStopped()
	}

	var b strings.Builder

	b.WriteString(m.styles.title.Render(title))
	b.WriteString("\n")
	b.WriteString(fitStatLine(m.styles, width,
		countField(m.text.Sent(), report.Sent, 1),
		statField{label: m.text.Errors(), value: m.errorShare(report.Sent, report.Failed)},
		statField{label: "rps", value: fmt.Sprintf("%.0f", float64(report.Sent)/max(report.Duration.Seconds(), 1)), drop: 2},
		statField{label: m.text.Latency(), value: formatDuration(report.Duration)},
	))
	b.WriteString("\n" + "\n")

	// Sent, errors and p99 always fit; p50 and p90 go first when the terminal
	// is narrow, and the method name takes whatever is left. Columns are sized
	// by width on screen and never narrower than their heading: "отправлено"
	// is ten columns, and a Chinese heading takes two per character.
	const (
		pColumn = 9
		minName = 12
	)

	sentColumn := max(9, lipgloss.Width(m.text.Sent()))
	errColumn := max(8, lipgloss.Width(m.text.Errors()))
	coreColumns := 1 + sentColumn + 1 + errColumn + 1 + pColumn
	midColumns := 1 + pColumn + 1 + pColumn

	wide := width-coreColumns-midColumns >= minName

	nameWidth := width - coreColumns
	if wide {
		nameWidth -= midColumns
	}
	nameWidth = min(max(nameWidth, minName), 40)

	header := padRight(m.text.ColumnMethod(), nameWidth) + " " +
		padLeft(m.text.Sent(), sentColumn) + " " + padLeft(m.text.Errors(), errColumn)
	if wide {
		header += " " + padLeft("p50", pColumn) + " " + padLeft("p90", pColumn)
	}
	header += " " + padLeft("p99", pColumn)

	b.WriteString(m.styles.label.Render(header))
	b.WriteString("\n")

	for i := range report.Methods {
		method := &report.Methods[i]
		name := truncate(shortMethod(method.Method), nameWidth)

		sent := formatCount(method.Sent)
		if lipgloss.Width(sent) > sentColumn {
			sent = compactCount(uint64(max(method.Sent, 0)))
		}

		errors := m.styles.value
		if method.Failed > 0 {
			errors = m.styles.bad
		}

		b.WriteString(m.styles.value.Render(padRight(name, nameWidth)))
		b.WriteString(m.styles.value.Render(" " + padLeft(sent, sentColumn)))
		b.WriteString(errors.Render(" " + padLeft(m.errorShare(method.Sent, method.Failed), errColumn)))
		if wide {
			b.WriteString(m.styles.muted.Render(" " + padLeft(formatQuantile(method.P50), pColumn) +
				" " + padLeft(formatQuantile(method.P90), pColumn)))
		}
		b.WriteString(m.styles.value.Render(" " + padLeft(formatQuantile(method.P99), pColumn)))
		b.WriteString("\n")
	}

	if m.stopper.Stopping() {
		b.WriteString("\n")
		b.WriteString(m.styles.note.Render("> " + m.text.ReportStoppedNote()))
	}

	if m.err != nil && !m.stopper.Stopping() {
		b.WriteString("\n")
		b.WriteString(m.styles.bad.Render("> " + m.err.Error()))
	}

	return b.String()
}

// note returns the live view's note in full and in the short form a narrow
// frame takes instead of wrapping it.
func (m *model) note() (full, short string) {
	s := m.snapshot

	if m.warmup > 0 && s.Elapsed < m.warmup {
		return m.text.WarmupNote(formatDuration(m.warmup - s.Elapsed)), m.text.WarmupNoteShort()
	}
	if s.Sent > 0 && float64(s.Failed)/float64(s.Sent) > 0.05 {
		return m.text.ErrorsNote(), m.text.ErrorsNoteShort()
	}
	if s.InFlight > 0 && float64(s.InFlight) > m.totalTarget() {
		return m.text.InFlightNote(), m.text.InFlightNoteShort()
	}

	return "", ""
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
	name := m.styles.faint.Render("LeetTest")

	gap := contentWidth(m.viewWidth()) - lipgloss.Width(hints) - lipgloss.Width(name)
	if gap < 2 {
		return hints
	}

	return hints + m.styles.pad(gap) + name
}

// padRight and padLeft pad s with spaces to width columns on screen; fmt
// pads by characters, which a Chinese character or a combining mark defeats.
func padRight(s string, width int) string {
	return s + strings.Repeat(" ", max(width-lipgloss.Width(s), 0))
}

func padLeft(s string, width int) string {
	return strings.Repeat(" ", max(width-lipgloss.Width(s), 0)) + s
}
