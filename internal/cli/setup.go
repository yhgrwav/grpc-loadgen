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
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type setupStep int

const (
	stepLang setupStep = iota
	stepTheme
	stepDone
)

type setupModel struct {
	step     setupStep
	cursor   int
	settings *Settings
	styles   styles
	text     Text
	frame    int
}

// RunSetup asks for the interface language and colour theme, then stores them.
func RunSetup(settings *Settings) error {
	m := &setupModel{
		settings: settings,
		styles:   newStyles(ThemeByName("")),
		text:     NewText(DetectLang()),
	}

	program := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := program.Run(); err != nil {
		return err
	}

	return settings.Save()
}

func (m *setupModel) Init() tea.Cmd {
	return tick()
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.frame++

		return m, tick()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "up", "k":
			m.cursor = max(m.cursor-1, 0)

		case "down", "j":
			m.cursor = min(m.cursor+1, m.options()-1)

		case "enter", " ":
			return m.choose()
		}
	}

	return m, nil
}

func (m *setupModel) options() int {
	if m.step == stepLang {
		return len(Languages())
	}

	return len(Themes())
}

func (m *setupModel) choose() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepLang:
		lang := Languages()[m.cursor].Lang
		m.settings.Lang = string(lang)
		m.text = NewText(lang)
		m.step = stepTheme
		m.cursor = 0

	case stepTheme:
		theme := Themes()[m.cursor]
		m.settings.Theme = theme.Name
		m.styles = newStyles(theme)
		m.step = stepDone

		return m, tea.Quit

	case stepDone:
		return m, tea.Quit
	}

	return m, nil
}

func (m *setupModel) View() string {
	var b strings.Builder

	b.WriteString(m.styles.shimmer("◆ grpc-loadgen", m.frame))
	b.WriteString("\n\n")

	title := m.text.PickLanguage()
	if m.step == stepTheme {
		title = m.text.PickTheme()
	}

	b.WriteString(m.styles.title.Render(title))
	b.WriteString("\n\n")

	for i, option := range m.entries() {
		if i == m.cursor {
			b.WriteString(m.styles.tabOn.Render("▸ " + option))
		} else {
			b.WriteString(m.styles.muted.Render("  " + option))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.styles.faint.Render(m.text.PickHint()))

	return m.styles.frame.Render(b.String())
}

func (m *setupModel) entries() []string {
	if m.step == stepLang {
		names := make([]string, 0, len(Languages()))
		for _, option := range Languages() {
			names = append(names, option.Title)
		}

		return names
	}

	themes := Themes()

	names := make([]string, 0, len(themes))
	for i := range themes {
		names = append(names, themes[i].Name)
	}

	return names
}
