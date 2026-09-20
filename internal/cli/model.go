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
	sparkWidth   = 36
)

type tickMsg time.Time

type doneMsg struct{ err error }

type history struct {
	rps []float64
	p99 []float64
}

func (h *history) push(rps float64, p99 time.Duration) {
	h.rps = appendCapped(h.rps, rps)
	h.p99 = appendCapped(h.p99, float64(p99.Milliseconds()))
}

func appendCapped(values []float64, v float64) []float64 {
	values = append(values, v)
	if len(values) > historyLimit {
		values = values[len(values)-historyLimit:]
	}

	return values
}

type model struct {
	target string
	engine *engine.Engine
	cancel func()

	text   Text
	styles styles
	theme  string

	snapshot  engine.Snapshot
	warmup    time.Duration
	overall   history
	perMethod map[string]*history

	tabs   []string
	active int

	frame    int
	width    int
	height   int
	showHelp bool
	command  string
	inCmd    bool
	notice   string
	stopping bool
	done     bool
	err      error

	settings *Settings
}

func newModel(target string, eng *engine.Engine, warmup time.Duration, settings *Settings, cancel func()) *model {
	text := NewText(Lang(settings.Lang))
	theme := ThemeByName(settings.Theme)

	calls := eng.Calls()

	tabs := make([]string, 0, len(calls)+1)
	tabs = append(tabs, text.Summary())

	for _, call := range calls {
		tabs = append(tabs, shortMethod(call.Method))
	}

	return &model{
		target:    target,
		engine:    eng,
		cancel:    cancel,
		text:      text,
		styles:    newStyles(theme),
		theme:     theme.Name,
		warmup:    warmup,
		perMethod: make(map[string]*history),
		tabs:      tabs,
		settings:  settings,
	}
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
		m.snapshot = m.engine.Snapshot()
		m.overall.push(m.snapshot.RPS, m.snapshot.P99)

		for _, method := range m.snapshot.Methods {
			h, ok := m.perMethod[method.Method]
			if !ok {
				h = &history{}
				m.perMethod[method.Method] = h
			}
			h.push(method.RPS, method.P99)
		}

		return m, tick()

	case doneMsg:
		m.snapshot = m.engine.Snapshot()
		m.done = true
		m.err = msg.err

		return m, tea.Quit

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	return m, nil
}

func (m *model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inCmd {
		return m.onCommandKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.stop()

		return m, nil

	case "tab", "right", "l":
		m.active = (m.active + 1) % len(m.tabs)

		return m, nil

	case "shift+tab", "left", "h":
		m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)

		return m, nil

	case "?":
		m.showHelp = !m.showHelp

		return m, nil

	case "/":
		m.inCmd = true
		m.command = "/"
		m.notice = ""

		return m, nil
	}

	return m, nil
}

func (m *model) onCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inCmd = false
		m.command = ""

	case "enter":
		m.runCommand(strings.TrimSpace(m.command))
		m.inCmd = false
		m.command = ""

	case "backspace":
		if len(m.command) > 1 {
			m.command = m.command[:len(m.command)-1]
		}

	default:
		if len(msg.String()) == 1 {
			m.command += msg.String()
		}
	}

	return m, nil
}

func (m *model) runCommand(cmd string) {
	name, arg, _ := strings.Cut(strings.TrimPrefix(cmd, "/"), " ")
	arg = strings.TrimSpace(arg)

	switch name {
	case "help":
		m.showHelp = true

	case "quit", "stop":
		m.stop()

	case "theme":
		m.applyTheme(arg)

	case "lang":
		m.applyLang(arg)

	default:
		m.notice = m.text.UnknownCommand(name)
	}
}

func (m *model) applyTheme(name string) {
	if name == "" {
		themes := Themes()
		for i := range themes {
			if themes[i].Name == m.theme {
				name = themes[(i+1)%len(themes)].Name

				break
			}
		}
	}

	theme := ThemeByName(name)
	m.styles = newStyles(theme)
	m.theme = theme.Name
	m.settings.Theme = theme.Name
	m.saveSettings()
}

func (m *model) applyLang(code string) {
	if code == "" {
		langs := Languages()
		for i, option := range langs {
			if option.Lang == m.text.Lang() {
				code = string(langs[(i+1)%len(langs)].Lang)

				break
			}
		}
	}

	m.text = NewText(Lang(code))
	m.settings.Lang = code
	m.tabs[0] = m.text.Summary()
	m.saveSettings()
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
