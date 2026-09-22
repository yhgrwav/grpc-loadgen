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
	stepMode
	stepPalette
)

type setupModel struct {
	step     setupStep
	cursor   int
	settings *Settings
	styles   styles
	text     Text
	frame    int
}

// RunSetup asks for the interface language, mode and palette, then stores them.
func RunSetup(settings *Settings) error {
	if settings.Mode == "" {
		settings.Mode = string(ModeDark)
	}
	if settings.Palette == "" {
		settings.Palette = Palettes()[0].Name
	}

	m := &setupModel{
		settings: settings,
		text:     NewText(DetectLang()),
	}
	m.restyle()

	program := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	if _, err := program.Run(); err != nil {
		return err
	}

	return settings.Save()
}

func (m *setupModel) restyle() {
	m.styles = newStyles(ThemeFor(m.settings.Palette, Mode(m.settings.Mode)))
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
		switch keyOf(msg) {
		case "ctrl+c", "esc", "q":
			return m, tea.Quit

		case "up", "k":
			m.cursor = wrap(m.cursor-1, m.options())
			m.preview()

		case "down", "j":
			m.cursor = wrap(m.cursor+1, m.options())
			m.preview()

		case "enter", " ", "right", "l":
			return m.choose()
		}
	}

	return m, nil
}

func (m *setupModel) options() int {
	switch m.step {
	case stepLang:
		return len(Languages())
	case stepMode:
		return 2
	case stepPalette:
		return len(Palettes())
	}

	return 1
}

func (m *setupModel) preview() {
	switch m.step {
	case stepLang:
		m.text = NewText(Languages()[m.cursor].Lang)

	case stepMode:
		m.settings.Mode = string(m.modeAt(m.cursor))
		m.restyle()

	case stepPalette:
		m.settings.Palette = Palettes()[m.cursor].Name
		m.restyle()
	}
}

func (m *setupModel) modeAt(index int) Mode {
	if index == 1 {
		return ModeLight
	}

	return ModeDark
}

func (m *setupModel) choose() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepLang:
		m.settings.Lang = string(Languages()[m.cursor].Lang)
		m.text = NewText(Lang(m.settings.Lang))
		m.step = stepMode
		m.cursor = 0
		m.preview()

	case stepMode:
		m.settings.Mode = string(m.modeAt(m.cursor))
		m.step = stepPalette
		m.cursor = 0
		m.preview()

	case stepPalette:
		m.settings.Palette = Palettes()[m.cursor].Name
		m.restyle()

		return m, tea.Quit
	}

	return m, nil
}

func (m *setupModel) View() string {
	var b strings.Builder

	b.WriteString(m.styles.shimmer("◆ LeetTest", m.frame))
	b.WriteString("\n\n")
	b.WriteString(m.styles.title.Render(m.stepTitle()))
	b.WriteString("\n\n")

	for i, option := range m.entries() {
		marker := m.styles.pick.Render("   ")
		style := m.styles.pick

		if i == m.cursor {
			marker = m.styles.pickOn.Render(" ▸ ")
			style = m.styles.pickOn
		}

		b.WriteString(marker + style.Render(option))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.styles.muted.Render(m.text.PickHint()))

	return m.styles.frame.Render(b.String())
}

func (m *setupModel) stepTitle() string {
	switch m.step {
	case stepLang:
		return m.text.PickLanguage()
	case stepMode:
		return m.text.PickTheme()
	case stepPalette:
		return m.text.PaletteRow()
	}

	return ""
}

func (m *setupModel) entries() []string {
	switch m.step {
	case stepLang:
		names := make([]string, 0, len(Languages()))
		for _, option := range Languages() {
			names = append(names, option.Title)
		}

		return names

	case stepMode:
		return []string{m.text.ModeDark(), m.text.ModeLight()}

	case stepPalette:
		palettes := Palettes()

		names := make([]string, 0, len(palettes))
		for i := range palettes {
			theme := palettes[i].Dark
			if Mode(m.settings.Mode) == ModeLight {
				theme = palettes[i].Light
			}

			names = append(names, m.styles.pick.Render(padRight(palettes[i].Name, 10))+swatch(theme))
		}

		return names
	}

	return nil
}

func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}

	return text + strings.Repeat(" ", width-len(text))
}
