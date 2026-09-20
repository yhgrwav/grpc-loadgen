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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

const refresh = 100 * time.Millisecond

var (
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	alertStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	barFilled  = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	barEmpty   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

type tickMsg time.Time

type doneMsg struct {
	err error
}

type model struct {
	target   string
	engine   *engine.Engine
	snapshot engine.Snapshot
	cancel   func()
	done     bool
	err      error
	stopping bool
}

// NewProgram builds the live view of a run.
func NewProgram(target string, eng *engine.Engine, run func() error, cancel func()) *tea.Program {
	m := &model{target: target, engine: eng, cancel: cancel}

	program := tea.NewProgram(m, tea.WithOutput(stderr()))

	go func() {
		err := run()
		program.Send(doneMsg{err: err})
	}()

	return program
}

func (m *model) Init() tea.Cmd {
	return tick()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.snapshot = m.engine.Snapshot()

		return m, tick()

	case doneMsg:
		m.snapshot = m.engine.Snapshot()
		m.done = true
		m.err = msg.err

		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if !m.stopping {
				m.stopping = true
				m.cancel()
			}

			return m, nil
		}
	}

	return m, nil
}

func (m *model) View() string {
	if m.done {
		return ""
	}

	s := m.snapshot

	var b strings.Builder

	b.WriteString(titleStyle.Render("grpc-loadgen"))
	b.WriteString(labelStyle.Render("  →  " + m.target))
	b.WriteString("\n\n")

	b.WriteString(progressBar(s.Elapsed, s.Total))
	b.WriteString("  ")
	b.WriteString(valueStyle.Render(formatElapsed(s.Elapsed, s.Total)))
	b.WriteString("\n\n")

	b.WriteString(field("sent", fmt.Sprintf("%d", s.Sent)))
	b.WriteString(field("rps", fmt.Sprintf("%.0f", s.RPS)))
	b.WriteString(field("in-flight", fmt.Sprintf("%d", s.InFlight)))
	b.WriteString(errField(s))
	b.WriteString(field("p99", formatDuration(s.P99)))
	b.WriteString("\n\n")

	if m.stopping {
		b.WriteString(alertStyle.Render("stopping, waiting for requests in flight"))
	} else {
		b.WriteString(hintStyle.Render("q to stop the run"))
	}
	b.WriteString("\n")

	return b.String()
}

func field(label, value string) string {
	return labelStyle.Render(label+" ") + valueStyle.Render(value) + labelStyle.Render("   ")
}

func errField(s engine.Snapshot) string {
	ratio := 0.0
	if s.Sent > 0 {
		ratio = float64(s.Failed) / float64(s.Sent) * 100
	}

	value := fmt.Sprintf("%.1f%%", ratio)
	if s.Failed > 0 {
		return labelStyle.Render("errors ") + alertStyle.Render(value) + labelStyle.Render("   ")
	}

	return field("errors", value)
}

func progressBar(elapsed, total time.Duration) string {
	const width = 32

	filled := 0
	if total > 0 {
		filled = int(float64(width) * float64(elapsed) / float64(total))
	}
	filled = min(max(filled, 0), width)

	return barFilled.Render(strings.Repeat("━", filled)) +
		barEmpty.Render(strings.Repeat("━", width-filled))
}

func formatElapsed(elapsed, total time.Duration) string {
	if total <= 0 {
		return formatDuration(elapsed)
	}

	return formatDuration(elapsed) + labelStyle.Render(" / ") + formatDuration(total)
}

func tick() tea.Cmd {
	return tea.Tick(refresh, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
