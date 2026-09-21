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

type Mode string

const (
	ModeDark  Mode = "dark"
	ModeLight Mode = "light"
)

type Theme struct {
	Bg      lipgloss.Color
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

type Palette struct {
	Name  string
	Dark  Theme
	Light Theme
}

const (
	darkBg  lipgloss.Color = "235"
	lightBg lipgloss.Color = "254"
)

// Palettes lists the colour schemes the tool ships with.
func Palettes() []Palette {
	return []Palette{
		{
			Name: "aurora",
			Dark: Theme{
				Bg: darkBg, Accent: "81", Text: "252", Muted: "247", Faint: "240",
				Good: "114", Warn: "179", Bad: "203", Border: "238",
				Shimmer: shades("39", "45", "51", "87", "123"),
			},
			Light: Theme{
				Bg: lightBg, Accent: "25", Text: "236", Muted: "241", Faint: "247",
				Good: "22", Warn: "94", Bad: "124", Border: "251",
				Shimmer: shades("25", "31", "38", "44", "37"),
			},
		},
		{
			Name: "ember",
			Dark: Theme{
				Bg: darkBg, Accent: "209", Text: "252", Muted: "247", Faint: "240",
				Good: "150", Warn: "215", Bad: "203", Border: "238",
				Shimmer: shades("166", "173", "180", "215", "222"),
			},
			Light: Theme{
				Bg: lightBg, Accent: "94", Text: "236", Muted: "241", Faint: "247",
				Good: "22", Warn: "58", Bad: "124", Border: "251",
				Shimmer: shades("130", "166", "172", "208", "214"),
			},
		},
		{
			Name: "forest",
			Dark: Theme{
				Bg: darkBg, Accent: "114", Text: "252", Muted: "247", Faint: "240",
				Good: "119", Warn: "179", Bad: "203", Border: "238",
				Shimmer: shades("22", "28", "35", "71", "114"),
			},
			Light: Theme{
				Bg: lightBg, Accent: "22", Text: "236", Muted: "241", Faint: "247",
				Good: "22", Warn: "94", Bad: "124", Border: "251",
				Shimmer: shades("22", "28", "34", "64", "70"),
			},
		},
		{
			Name: "violet",
			Dark: Theme{
				Bg: darkBg, Accent: "141", Text: "252", Muted: "247", Faint: "240",
				Good: "114", Warn: "179", Bad: "204", Border: "238",
				Shimmer: shades("55", "92", "98", "141", "183"),
			},
			Light: Theme{
				Bg: lightBg, Accent: "91", Text: "236", Muted: "241", Faint: "247",
				Good: "22", Warn: "94", Bad: "125", Border: "251",
				Shimmer: shades("54", "91", "97", "104", "134"),
			},
		},
		{
			Name: "mono",
			Dark: Theme{
				Bg: darkBg, Accent: "255", Text: "252", Muted: "247", Faint: "239",
				Good: "252", Warn: "248", Bad: "231", Border: "237",
				Shimmer: shades("240", "244", "248", "252", "255"),
			},
			Light: Theme{
				Bg: lightBg, Accent: "235", Text: "236", Muted: "240", Faint: "250",
				Good: "238", Warn: "240", Bad: "232", Border: "252",
				Shimmer: shades("250", "246", "242", "238", "235"),
			},
		},
	}
}

func shades(colors ...lipgloss.Color) []lipgloss.Color {
	wave := make([]lipgloss.Color, 0, len(colors)*2-2)
	wave = append(wave, colors...)

	for i := len(colors) - 2; i > 0; i-- {
		wave = append(wave, colors[i])
	}

	return wave
}

// PaletteByName returns the named palette, falling back to the first one.
func PaletteByName(name string) Palette {
	palettes := Palettes()
	for i := range palettes {
		if palettes[i].Name == name {
			return palettes[i]
		}
	}

	return palettes[0]
}

// ThemeFor resolves a palette and mode into the colours to draw with.
func ThemeFor(name string, mode Mode) Theme {
	palette := PaletteByName(name)
	if mode == ModeLight {
		return palette.Light
	}

	return palette.Dark
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
	pick     lipgloss.Style
	pickOn   lipgloss.Style
}

func newStyles(theme Theme) styles {
	bg := func(style lipgloss.Style) lipgloss.Style {
		return style.Background(theme.Bg)
	}

	return styles{
		theme:    theme,
		title:    bg(lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)),
		label:    bg(lipgloss.NewStyle().Foreground(theme.Muted)),
		value:    bg(lipgloss.NewStyle().Foreground(theme.Text).Bold(true)),
		muted:    bg(lipgloss.NewStyle().Foreground(theme.Muted)),
		faint:    bg(lipgloss.NewStyle().Foreground(theme.Faint)),
		good:     bg(lipgloss.NewStyle().Foreground(theme.Good)),
		warn:     bg(lipgloss.NewStyle().Foreground(theme.Warn)),
		bad:      lipgloss.NewStyle().Foreground(theme.Bad).Bold(true),
		tab:      lipgloss.NewStyle().Foreground(theme.Muted).Padding(0, 2),
		tabOn:    bg(lipgloss.NewStyle().Foreground(theme.Accent).Bold(true).Padding(0, 2).Underline(true)),
		frame:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Border).BorderBackground(theme.Bg).Background(theme.Bg).Foreground(theme.Text).Padding(1, 2),
		note:     bg(lipgloss.NewStyle().Foreground(theme.Warn).Italic(true)),
		barOn:    bg(lipgloss.NewStyle().Foreground(theme.Accent)),
		barOff:   bg(lipgloss.NewStyle().Foreground(theme.Border)),
		spark:    bg(lipgloss.NewStyle().Foreground(theme.Accent)),
		helpKey:  bg(lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)),
		helpText: bg(lipgloss.NewStyle().Foreground(theme.Muted)),
		pick:     bg(lipgloss.NewStyle().Foreground(theme.Muted)),
		pickOn:   bg(lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)),
	}
}

func (s styles) pad(width int) string {
	if width < 1 {
		return ""
	}

	return s.faint.Render(strings.Repeat(" ", width))
}

func (s styles) shimmer(text string, frame int) string {
	colors := s.theme.Shimmer
	if len(colors) == 0 {
		return s.title.Render(text)
	}

	var b strings.Builder

	for i, r := range text {
		color := colors[(i+frame)%len(colors)]
		b.WriteString(lipgloss.NewStyle().Foreground(color).Background(s.theme.Bg).Bold(true).Render(string(r)))
	}

	return b.String()
}

func swatch(theme Theme) string {
	colors := []lipgloss.Color{theme.Accent, theme.Good, theme.Warn, theme.Bad, theme.Muted}

	var b strings.Builder

	for _, color := range colors {
		b.WriteString(lipgloss.NewStyle().Foreground(color).Background(theme.Bg).Render("██"))
	}

	return b.String()
}
