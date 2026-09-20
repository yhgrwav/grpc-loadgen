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

// NewProgram builds the full-screen view of a run.
func NewProgram(target string, eng *engine.Engine, warmup time.Duration, settings *Settings, run func() error, cancel func()) *tea.Program {
	m := newModel(target, eng, warmup, settings, cancel)

	program := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
	)

	go func() {
		program.Send(doneMsg{err: run()})
	}()

	return program
}

func (m *model) View() string {
	if m.done {
		return ""
	}

	width := m.width
	if width < 40 {
		width = 72
	}
	inner := width - 6

	var b strings.Builder

	b.WriteString(m.header(inner))
	b.WriteString("\n\n")
	b.WriteString(m.tabBar(inner))
	b.WriteString("\n\n")

	switch {
	case m.showHelp:
		b.WriteString(m.help())
	case m.active == 0:
		b.WriteString(m.summary(inner))
	default:
		b.WriteString(m.method(inner, m.active-1))
	}

	b.WriteString("\n\n")
	b.WriteString(m.footer())

	return m.styles.frame.Width(width - 2).Render(b.String())
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

	left := m.styles.shimmer(glyph+" grpc-loadgen", m.frame) +
		m.styles.faint.Render("  ·  ") +
		m.styles.muted.Render(m.text.Target()+" ") +
		m.styles.value.Render(m.target)

	right := m.styles.muted.Render(status)

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	s := m.snapshot

	return left + strings.Repeat(" ", gap) + right + "\n" +
		progress(m.styles, s.Elapsed, s.Total, width) + " " +
		m.styles.muted.Render(formatDuration(s.Elapsed)+" / "+formatDuration(s.Total))
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
		[2]string{"p99", formatDuration(s.P99)},
	))
	b.WriteString("\n\n")

	b.WriteString(m.gaugeRow("rps", s.RPS, m.totalTarget(), fmt.Sprintf("%.0f", s.RPS)))
	b.WriteString("\n")
	b.WriteString(m.gaugeRow(m.text.InFlight(), float64(s.InFlight), float64(max(s.InFlight, 1)*2), formatCount(s.InFlight)))
	b.WriteString("\n\n")

	b.WriteString(m.styles.label.Render(m.text.Rate()+"  ") + sparkline(m.styles, m.overall.rps, sparkWidth))
	b.WriteString("\n")
	b.WriteString(m.styles.label.Render(m.text.Latency()+"  ") + sparkline(m.styles, m.overall.p99, sparkWidth))

	if note := m.note(); note != "" {
		b.WriteString("\n\n")
		b.WriteString(m.styles.note.Render("› " + note))
	}

	return b.String()
}

func (m *model) method(width, index int) string {
	if index >= len(m.snapshot.Methods) {
		return m.styles.faint.Render("…")
	}

	method := m.snapshot.Methods[index]

	var b strings.Builder

	b.WriteString(m.styles.value.Render(method.Method))
	b.WriteString("\n\n")

	b.WriteString(statLine(m.styles,
		[2]string{m.text.Sent(), formatCount(method.Sent)},
		[2]string{"rps", fmt.Sprintf("%.0f", method.RPS)},
		[2]string{m.text.Errors(), m.errorShare(method.Sent, method.Failed)},
		[2]string{"p50", formatDuration(method.P50)},
		[2]string{"p99", formatDuration(method.P99)},
	))
	b.WriteString("\n\n")

	b.WriteString(m.gaugeRow("rps", method.RPS, float64(method.TargetRPS), fmt.Sprintf("%.0f / %d", method.RPS, method.TargetRPS)))
	b.WriteString("\n\n")

	h := m.perMethod[method.Method]
	if h == nil {
		h = &history{}
	}

	b.WriteString(m.styles.label.Render(m.text.Rate()+"  ") + sparkline(m.styles, h.rps, sparkWidth))
	b.WriteString("\n")
	b.WriteString(m.styles.label.Render(m.text.Latency()+"  ") + sparkline(m.styles, h.p99, sparkWidth))

	return b.String()
}

func (m *model) gaugeRow(label string, value, limit float64, text string) string {
	return m.styles.label.Render(fmt.Sprintf("%-10s", label)) +
		gauge(m.styles, value, limit, gaugeWidth) + "  " +
		m.styles.value.Render(text)
}

func (m *model) help() string {
	rows := [][2]string{
		{"tab", m.text.HelpTabs()},
		{"?", m.text.HelpHelp()},
		{"q", m.text.HelpQuit()},
		{"/", m.text.HelpCommands()},
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

func (m *model) footer() string {
	if m.inCmd {
		return m.styles.title.Render(m.command) + m.styles.faint.Render("▏")
	}

	if m.notice != "" {
		return m.styles.note.Render(m.notice)
	}

	return keyHint(m.styles, m.text.HintTabs(), m.text.HintHelp(), m.text.HintCommands(), m.text.HintQuit())
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
