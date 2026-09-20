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
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

const (
	refresh      = 120 * time.Millisecond
	historyLimit = 240
	gaugeWidth   = 24
	sparkWidth   = 48
)

type tickMsg time.Time

type doneMsg struct{ err error }

type history struct {
	rps    []float64
	points []point
}

func (h *history) push(rps float64, p50, p90, p99 time.Duration) {
	h.rps = appendCapped(h.rps, rps)

	h.points = append(h.points, point{
		p50: float64(p50.Microseconds()) / 1000,
		p90: float64(p90.Microseconds()) / 1000,
		p99: float64(p99.Microseconds()) / 1000,
	})

	if len(h.points) > historyLimit {
		h.points = h.points[len(h.points)-historyLimit:]
	}
}

func appendCapped(values []float64, v float64) []float64 {
	values = append(values, v)
	if len(values) > historyLimit {
		values = values[len(values)-historyLimit:]
	}

	return values
}

type settingsRow int

const (
	rowLang settingsRow = iota
	rowMode
	rowPalette
	settingsRows
)

type model struct {
	target string
	engine *engine.Engine
	cancel func()

	text   Text
	styles styles

	snapshot  engine.Snapshot
	report    engine.Report
	warmup    time.Duration
	overall   history
	perMethod map[string]*history

	tabs   []string
	active int
	row    settingsRow

	frame    int
	width    int
	height   int
	showHelp bool
	editing  bool
	notice   string
	stopping bool
	done     bool
	err      error

	settings *Settings
}

func newModel(target string, eng *engine.Engine, warmup time.Duration, settings *Settings, cancel func()) *model {
	m := &model{
		target:    target,
		engine:    eng,
		cancel:    cancel,
		warmup:    warmup,
		perMethod: make(map[string]*history),
		settings:  settings,
	}

	m.applySettings()
	m.buildTabs(eng)

	return m
}

func (m *model) applySettings() {
	m.text = NewText(Lang(m.settings.Lang))
	m.styles = newStyles(ThemeFor(m.settings.Palette, Mode(m.settings.Mode)))
}

func (m *model) buildTabs(eng *engine.Engine) {
	calls := eng.Calls()

	m.tabs = make([]string, 0, len(calls)+2)
	m.tabs = append(m.tabs, m.text.Summary())

	for _, call := range calls {
		m.tabs = append(m.tabs, shortMethod(call.Method))
	}

	m.tabs = append(m.tabs, m.text.Settings())
}

func (m *model) settingsTab() int {
	return len(m.tabs) - 1
}

func (m *model) Init() tea.Cmd {
	return tick()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

		return m, nil

	case tickMsg:
		m.frame++

		if !m.done {
			m.snapshot = m.engine.Snapshot()
			m.overall.push(m.snapshot.RPS, m.snapshot.P50, m.snapshot.P90, m.snapshot.P99)

			for _, method := range m.snapshot.Methods {
				h, ok := m.perMethod[method.Method]
				if !ok {
					h = &history{}
					m.perMethod[method.Method] = h
				}
				h.push(method.RPS, method.P50, method.P90, method.P99)
			}
		}

		return m, tick()

	case doneMsg:
		m.snapshot = m.engine.Snapshot()
		m.report = m.engine.Report()
		m.done = true
		m.err = msg.err

		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	return m, nil
}

func (m *model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.done {
		switch key {
		case "enter", "q", "esc", "ctrl+c", " ":
			return m, tea.Quit
		}

		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		m.stop()

		return m, nil

	case "?":
		m.showHelp = !m.showHelp

		return m, nil

	case "esc":
		switch {
		case m.showHelp:
			m.showHelp = false
		case m.editing:
			m.editing = false
			m.notice = ""
		default:
			m.active = 0
		}

		return m, nil
	}

	if m.editing {
		return m.onSettingsKey(key)
	}

	if m.active == m.settingsTab() && (key == "enter" || key == " ") {
		m.editing = true

		return m, nil
	}

	switch key {
	case "right", "l":
		m.active = (m.active + 1) % len(m.tabs)
	case "left", "h":
		m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
	}

	return m, nil
}

func (m *model) onSettingsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.row = (m.row - 1 + settingsRows) % settingsRows

	case "down", "j":
		m.row = (m.row + 1) % settingsRows

	case "right", "l", "enter", " ":
		m.cycleSetting(1)

	case "left", "h":
		m.cycleSetting(-1)
	}

	return m, nil
}

func (m *model) cycleSetting(step int) {
	switch m.row {
	case rowLang:
		langs := Languages()
		index := 0

		for i, option := range langs {
			if string(option.Lang) == m.settings.Lang {
				index = i

				break
			}
		}

		m.settings.Lang = string(langs[wrap(index+step, len(langs))].Lang)

	case rowMode:
		if Mode(m.settings.Mode) == ModeLight {
			m.settings.Mode = string(ModeDark)
		} else {
			m.settings.Mode = string(ModeLight)
		}

	case rowPalette:
		palettes := Palettes()
		index := 0

		for i := range palettes {
			if palettes[i].Name == m.settings.Palette {
				index = i

				break
			}
		}

		m.settings.Palette = palettes[wrap(index+step, len(palettes))].Name
	}

	m.applySettings()
	m.tabs[0] = m.text.Summary()
	m.tabs[m.settingsTab()] = m.text.Settings()
	m.saveSettings()
}

func wrap(index, length int) int {
	return ((index % length) + length) % length
}

func (m *model) saveSettings() {
	if err := m.settings.Save(); err != nil {
		m.notice = err.Error()

		return
	}

	m.notice = m.text.Saved(m.settings.Path())
}

func (m *model) stop() {
	if m.stopping {
		return
	}

	m.stopping = true
	m.cancel()
}

func tick() tea.Cmd {
	return tea.Tick(refresh, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func shortMethod(method string) string {
	_, name, found := strings.Cut(method, "/")
	if !found {
		return method
	}

	return name
}
