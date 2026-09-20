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

	"github.com/charmbracelet/lipgloss"
)

type Theme struct {
	Name string

	Accent  lipgloss.Color
	Text    lipgloss.Color
	Muted   lipgloss.Color
	Faint   lipgloss.Color
	Good    lipgloss.Color
	Warn    lipgloss.Color
	Bad     lipgloss.Color
	Border  lipgloss.Color
	Shimmer []lipgloss.Color
}

// Themes lists the colour themes the tool ships with.
func Themes() []Theme {
	return []Theme{
		{
			Name:   "aurora",
			Accent: "81", Text: "252", Muted: "245", Faint: "240",
			Good: "114", Warn: "179", Bad: "203", Border: "238",
			Shimmer: []lipgloss.Color{"39", "45", "51", "87", "123", "87", "51", "45"},
		},
		{
			Name:   "ember",
			Accent: "209", Text: "252", Muted: "245", Faint: "240",
			Good: "150", Warn: "215", Bad: "203", Border: "238",
			Shimmer: []lipgloss.Color{"166", "173", "180", "215", "222", "215", "180", "173"},
		},
		{
			Name:   "mono",
			Accent: "255", Text: "252", Muted: "245", Faint: "239",
			Good: "252", Warn: "248", Bad: "231", Border: "237",
			Shimmer: []lipgloss.Color{"240", "244", "248", "252", "255", "252", "248", "244"},
		},
	}
}

// ThemeByName returns the named theme, falling back to the first one.
func ThemeByName(name string) Theme {
	themes := Themes()
	for i := range themes {
		if themes[i].Name == name {
			return themes[i]
		}
	}

	return Themes()[0]
}

type styles struct {
	theme Theme

	title    lipgloss.Style
	label    lipgloss.Style
	value    lipgloss.Style
	muted    lipgloss.Style
	faint    lipgloss.Style
	good     lipgloss.Style
	warn     lipgloss.Style
	bad      lipgloss.Style
	tab      lipgloss.Style
	tabOn    lipgloss.Style
	frame    lipgloss.Style
	note     lipgloss.Style
	barOn    lipgloss.Style
	barOff   lipgloss.Style
	spark    lipgloss.Style
	helpKey  lipgloss.Style
	helpText lipgloss.Style
}

func newStyles(theme Theme) styles {
	return styles{
		theme:    theme,
		title:    lipgloss.NewStyle().Foreground(theme.Accent).Bold(true),
		label:    lipgloss.NewStyle().Foreground(theme.Muted),
		value:    lipgloss.NewStyle().Foreground(theme.Text).Bold(true),
		muted:    lipgloss.NewStyle().Foreground(theme.Muted),
		faint:    lipgloss.NewStyle().Foreground(theme.Faint),
		good:     lipgloss.NewStyle().Foreground(theme.Good),
		warn:     lipgloss.NewStyle().Foreground(theme.Warn),
		bad:      lipgloss.NewStyle().Foreground(theme.Bad).Bold(true),
		tab:      lipgloss.NewStyle().Foreground(theme.Muted).Padding(0, 2),
		tabOn:    lipgloss.NewStyle().Foreground(theme.Accent).Bold(true).Padding(0, 2).Underline(true),
		frame:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Border).Padding(0, 2),
		note:     lipgloss.NewStyle().Foreground(theme.Warn).Italic(true),
		barOn:    lipgloss.NewStyle().Foreground(theme.Accent),
		barOff:   lipgloss.NewStyle().Foreground(theme.Border),
		spark:    lipgloss.NewStyle().Foreground(theme.Accent),
		helpKey:  lipgloss.NewStyle().Foreground(theme.Accent).Bold(true),
		helpText: lipgloss.NewStyle().Foreground(theme.Muted),
	}
}

func (s styles) shimmer(text string, frame int) string {
	colors := s.theme.Shimmer
	if len(colors) == 0 {
		return s.title.Render(text)
	}

	var b strings.Builder

	for i, r := range text {
		color := colors[(i+frame)%len(colors)]
		b.WriteString(lipgloss.NewStyle().Foreground(color).Bold(true).Render(string(r)))
	}

	return b.String()
}
