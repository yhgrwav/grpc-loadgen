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
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

const (
	refresh = 120 * time.Millisecond
	// percentileEvery is how often the live view recomputes percentiles; the
	// counters move on every frame.
	percentileEvery = time.Second
	historyLimit    = 240
	gaugeWidth      = 24
)

type tickMsg time.Time

type doneMsg struct{ err error }

type history struct {
	rps    []float64
	points []point
}

func (h *history) push(rps float64, p50, p90, p99 metrics.Quantile) {
	h.rps = appendCapped(h.rps, rps)

	h.points = append(h.points, point{
		p50: plotted(p50), bound50: isBound(p50),
		p90: plotted(p90), bound90: isBound(p90),
		p99: plotted(p99), bound99: isBound(p99),
	})

	if len(h.points) > historyLimit {
		h.points = h.points[len(h.points)-historyLimit:]
	}
}

// plotted turns a quantile into a plottable value, or NaN when nothing was
// measured, so the chart shows a gap instead of a drop to zero.
func plotted(q metrics.Quantile) float64 {
	if !q.Defined {
		return math.NaN()
	}

	return float64(q.Value.Microseconds()) / 1000
}

// isBound says the quantile is only a lower bound: the request it would be was
// abandoned at the timeout.
func isBound(q metrics.Quantile) bool {
	return q.Defined && !q.Exact
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
	// service is what the header names: the one service of the config, or
	// the collection name.
	service string
	engine  *engine.Engine
	stopper *Stopper

	text   Text
	styles styles

	snapshot engine.Snapshot
	live     *engine.LiveBuffer
	// percentilesAt is when the snapshot's percentiles were last recomputed.
	// Copying a distribution holds its lock while recording waits, so it is
	// done once a second; a person cannot tell that from every frame.
	percentilesAt time.Time
	report        engine.Report
	unchecked     []Unchecked
	// finished is what the CLI knows of the finished run beside the engine report.
	finished RunReport
	// reportOf is the finished run's report, called once the run returns.
	reportOf  func() RunReport
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

	// hintKey is the last key that was no command, shown until hintStage
	// runs out; hintAt is the frame it was pressed on.
	hintKey string
	hintAt  int
	done    bool
	err     error

	settings *Settings
}

func newModel(target string, eng *engine.Engine, warmup time.Duration, settings *Settings, stopper *Stopper) *model {
	m := &model{
		target:    target,
		engine:    eng,
		stopper:   stopper,
		warmup:    warmup,
		perMethod: make(map[string]*history),
		live:      engine.NewLiveBuffer(),
		settings:  settings,
		reportOf:  func() RunReport { return RunReport{Report: eng.Report()} },
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
			fresh := time.Since(m.percentilesAt) >= percentileEvery
			m.engine.SnapshotInto(&m.snapshot, m.live, fresh)
			if fresh {
				m.percentilesAt = time.Now()
			}

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
		m.engine.SnapshotInto(&m.snapshot, m.live, true)
		run := m.reportOf()
		m.report, m.unchecked, m.finished = run.Report, run.Unchecked, run
		m.done = true
		m.err = msg.err

		// A q or a SIGTERM during the run asked to leave; the stop it started is over.
		if m.stopper.Stopping() {
			return m, tea.Quit
		}

		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	return m, nil
}

func (m *model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := keyOf(msg)

	// Any key replaces the hint: a command clears it, another unknown key
	// restarts it.
	m.hintKey = ""

	if m.done {
		switch key {
		case "enter", "q", "esc", "ctrl+c", " ":
			return m, tea.Quit
		}

		if !m.moveTab(key) {
			m.unknownKey(msg)
		}

		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		// One press stops the run and leaves once the calls in flight drain; a
		// second aborts them and leaves now; a third exits without a report.
		if m.stop() >= StageAbort {
			return m, tea.Quit
		}

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
		if !m.onSettingsKey(key) {
			m.unknownKey(msg)
		}

		return m, nil
	}

	if m.active == m.settingsTab() && (key == "enter" || key == " ") {
		m.editing = true

		return m, nil
	}

	if !m.moveTab(key) {
		m.unknownKey(msg)
	}

	return m, nil
}

func (m *model) moveTab(key string) bool {
	switch key {
	case "right", "l", "tab":
		m.active = (m.active + 1) % len(m.tabs)
	case "left", "h", "shift+tab":
		m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
	default:
		return false
	}

	return true
}

func (m *model) onSettingsKey(key string) bool {
	switch key {
	case "up", "k":
		m.row = (m.row - 1 + settingsRows) % settingsRows

	case "down", "j":
		m.row = (m.row + 1) % settingsRows

	case "right", "l", "enter", " ":
		m.cycleSetting(1)

	case "left", "h":
		m.cycleSetting(-1)

	default:
		return false
	}

	return true
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

func (m *model) stop() StopStage {
	return m.stopper.Press()
}

func tick() tea.Cmd {
	return tea.Tick(refresh, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func shortMethod(method string) string {
	_, name, found := strings.Cut(displayMethod(method), "/")
	if !found {
		return displayMethod(method)
	}

	return name
}
