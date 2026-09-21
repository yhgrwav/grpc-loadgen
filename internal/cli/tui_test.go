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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if settings.Configured() {
		t.Fatal("fresh settings report themselves as configured")
	}

	settings.Lang = string(LangRU)
	settings.Mode = string(ModeLight)
	settings.Palette = "ember"

	if saveErr := settings.Save(); saveErr != nil {
		t.Fatalf("save: %v", saveErr)
	}

	again, err := LoadSettings()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if !again.Configured() {
		t.Error("saved settings do not report themselves as configured")
	}
	if again.Lang != string(LangRU) || again.Palette != "ember" || again.Mode != string(ModeLight) {
		t.Errorf("reloaded %q/%q/%q, want ru/light/ember", again.Lang, again.Mode, again.Palette)
	}
	if filepath.Dir(again.Path()) == "" {
		t.Error("settings path is empty")
	}
	if _, err := os.Stat(again.Path()); err != nil {
		t.Errorf("settings file missing: %v", err)
	}
}

func TestDetectLang(t *testing.T) {
	tests := []struct {
		env  string
		want Lang
	}{
		{env: "ru_RU.UTF-8", want: LangRU},
		{env: "de_DE.UTF-8", want: LangDE},
		{env: "zh_CN.UTF-8", want: LangZH},
		{env: "en_US.UTF-8", want: LangEN},
		{env: "", want: LangEN},
	}

	for _, tt := range tests {
		t.Setenv("LC_ALL", tt.env)
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", "")

		if got := DetectLang(); got != tt.want {
			t.Errorf("DetectLang() with %q = %q, want %q", tt.env, got, tt.want)
		}
	}
}

func TestTextFallsBackToEnglish(t *testing.T) {
	text := NewText("xx")

	if got := text.Summary(); got != "summary" {
		t.Errorf("summary for an unknown language = %q, want the English text", got)
	}
}

func TestEveryLanguageTranslatesTheBasics(t *testing.T) {
	for _, option := range Languages() {
		text := NewText(option.Lang)

		for name, got := range map[string]string{
			"summary":   text.Summary(),
			"running":   text.Running(),
			"sent":      text.Sent(),
			"errors":    text.Errors(),
			"inflight":  text.InFlight(),
			"helptabs":  text.HelpTabs(),
			"helpquit":  text.HelpQuit(),
			"quitagain": text.HelpQuitAgain(),
		} {
			if got == "" {
				t.Errorf("%s is empty in %s", name, option.Lang)
			}
		}
	}
}

func TestPaletteByNameFallsBack(t *testing.T) {
	if got := PaletteByName("nope").Name; got != Palettes()[0].Name {
		t.Errorf("unknown palette resolved to %q, want the first one", got)
	}
	if got := PaletteByName("ember").Name; got != "ember" {
		t.Errorf("PaletteByName(ember) = %q", got)
	}
}

func TestEveryPaletteDefinesBothModes(t *testing.T) {
	palettes := Palettes()

	if len(palettes) != 5 {
		t.Fatalf("palettes = %d, want 5", len(palettes))
	}

	for i := range palettes {
		for mode, theme := range map[Mode]Theme{ModeDark: palettes[i].Dark, ModeLight: palettes[i].Light} {
			if theme.Accent == "" || theme.Text == "" || theme.Border == "" {
				t.Errorf("palette %s is incomplete in %s mode", palettes[i].Name, mode)
			}
			if len(theme.Shimmer) == 0 {
				t.Errorf("palette %s has no shimmer colours in %s mode", palettes[i].Name, mode)
			}
		}
	}
}

func TestThemeForPicksMode(t *testing.T) {
	dark := ThemeFor("aurora", ModeDark)
	light := ThemeFor("aurora", ModeLight)

	if dark.Accent == light.Accent {
		t.Error("dark and light modes share the same accent colour")
	}
	if got := ThemeFor("aurora", "whatever"); got.Accent != dark.Accent {
		t.Error("an unknown mode does not fall back to dark")
	}
}

func TestSparklineFitsWidth(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	values := make([]float64, 0, 100)
	for i := range 100 {
		values = append(values, float64(i))
	}

	if got := lipglossWidth(sparkline(s, values, 20)); got != 20 {
		t.Errorf("sparkline width = %d, want 20", got)
	}
	if got := lipglossWidth(sparkline(s, nil, 20)); got != 20 {
		t.Errorf("empty sparkline width = %d, want 20", got)
	}
}

func TestGaugeClampsToWidth(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	if got := lipglossWidth(gauge(s, 500, 100, 10)); got != 10 {
		t.Errorf("gauge width with an over-limit value = %d, want 10", got)
	}
	if got := lipglossWidth(gauge(s, -5, 100, 10)); got != 10 {
		t.Errorf("gauge width with a negative value = %d, want 10", got)
	}
}

func TestProgressAtEdges(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	if got := lipglossWidth(progress(s, 0, time.Minute, 16)); got != 16 {
		t.Errorf("progress width at the start = %d, want 16", got)
	}
	if got := lipglossWidth(progress(s, time.Hour, time.Minute, 16)); got != 16 {
		t.Errorf("progress width past the end = %d, want 16", got)
	}
}

func TestFormatCount(t *testing.T) {
	tests := map[int]string{
		0:       "0",
		999:     "999",
		1000:    "1000",
		25600:   "25 600",
		1234567: "1 234 567",
	}

	for in, want := range tests {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSparklineUsesTheValueRange(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	flat := make([]float64, 20)
	for i := range flat {
		flat[i] = 848
	}

	full := sparkline(s, flat, 20)
	if strings.Count(full, "█") > 0 {
		t.Error("a flat series is drawn as a full bar instead of a middle line")
	}

	rising := []float64{10, 20, 30, 40, 50}
	if got := lipglossWidth(sparkline(s, rising, 20)); got != 20 {
		t.Errorf("sparkline width = %d, want 20", got)
	}
}

func testModel(t *testing.T) *model {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)

	eng, err := engine.New(engine.Options{
		Calls: []engine.Call{
			{Method: "pkg.Svc/One", Timeout: time.Second, Stages: []engine.Stage{{TargetRPS: 1, Duration: time.Second}}},
			{Method: "pkg.Svc/Two", Timeout: time.Second, Stages: []engine.Stage{{TargetRPS: 1, Duration: time.Second}}},
		},
		Sender:      engine.FakeSender{},
		MaxInFlight: 2,
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	settings.Lang = string(LangEN)
	settings.Mode = string(ModeDark)
	settings.Palette = "aurora"

	return newModel("localhost:50051", eng, 0, settings, func() {})
}

func press(m *model, keys ...string) {
	for _, key := range keys {
		m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(key)}))
	}
}

func pressKey(m *model, types ...tea.KeyType) {
	for _, keyType := range types {
		m.onKey(tea.KeyMsg(tea.Key{Type: keyType}))
	}
}

func TestArrowsWalkEveryTab(t *testing.T) {
	m := testModel(t)

	if len(m.tabs) != 4 {
		t.Fatalf("tabs = %d, want summary + 2 methods + settings", len(m.tabs))
	}

	for want := 1; want < len(m.tabs); want++ {
		pressKey(m, tea.KeyRight)

		if m.active != want {
			t.Fatalf("after %d right presses active = %d, want %d", want, m.active, want)
		}
		if m.editing {
			t.Fatalf("walking to tab %d entered the settings", want)
		}
	}

	pressKey(m, tea.KeyRight)

	if m.active != 0 {
		t.Errorf("right from the last tab = %d, want a wrap to 0", m.active)
	}
}

func TestSettingsTabLetsArrowsThrough(t *testing.T) {
	m := testModel(t)
	m.active = m.settingsTab()

	pressKey(m, tea.KeyLeft)

	if m.active != m.settingsTab()-1 {
		t.Errorf("left on the settings tab = %d, want %d", m.active, m.settingsTab()-1)
	}
}

func TestEnterEntersSettingsAndEscLeaves(t *testing.T) {
	m := testModel(t)
	m.active = m.settingsTab()

	pressKey(m, tea.KeyEnter)

	if !m.editing {
		t.Fatal("enter on the settings tab did not enter the settings")
	}

	pressKey(m, tea.KeyRight)

	if m.active != m.settingsTab() {
		t.Errorf("right inside the settings moved to tab %d", m.active)
	}

	pressKey(m, tea.KeyEsc)

	if m.editing {
		t.Fatal("esc did not leave the settings")
	}

	pressKey(m, tea.KeyLeft)

	if m.active == m.settingsTab() {
		t.Error("arrows stay dead after leaving the settings")
	}
}

func TestEnterDoesNothingOutsideTheSettingsTab(t *testing.T) {
	m := testModel(t)

	pressKey(m, tea.KeyEnter)

	if m.editing {
		t.Error("enter on the summary tab entered the settings")
	}
}

func TestSettingsKeysChangeValuesOnlyWhileEditing(t *testing.T) {
	m := testModel(t)
	m.active = m.settingsTab()

	before := m.settings.Mode

	press(m, "j", "l")

	if m.settings.Mode != before || m.row != rowLang {
		t.Error("settings changed without entering them")
	}

	m.active = m.settingsTab()
	pressKey(m, tea.KeyEnter)
	press(m, "j")
	pressKey(m, tea.KeyRight)

	if m.row != rowMode {
		t.Errorf("row = %d, want the mode row", m.row)
	}
	if m.settings.Mode == before {
		t.Error("the mode did not change inside the settings")
	}
}

func TestEscFromAMethodTabGoesToTheSummary(t *testing.T) {
	m := testModel(t)
	m.active = 2

	pressKey(m, tea.KeyEsc)

	if m.active != 0 {
		t.Errorf("active = %d, want the summary", m.active)
	}
}

func TestTabKeyWalksTabs(t *testing.T) {
	m := testModel(t)

	pressKey(m, tea.KeyTab)
	if m.active != 1 {
		t.Fatalf("tab left the cursor on %d, want 1", m.active)
	}

	pressKey(m, tea.KeyShiftTab)
	if m.active != 0 {
		t.Fatalf("shift+tab left the cursor on %d, want 0", m.active)
	}
}

func TestTabsStillWalkAfterTheRun(t *testing.T) {
	m := testModel(t)
	m.done = true

	pressKey(m, tea.KeyRight)
	if m.active != 1 {
		t.Fatalf("right on the report left the cursor on %d, want 1", m.active)
	}

	pressKey(m, tea.KeyLeft)
	if m.active != 0 {
		t.Fatalf("left on the report left the cursor on %d, want 0", m.active)
	}
}

func TestQuitKeysStillLeaveTheReport(t *testing.T) {
	for _, key := range []string{"q", "enter", " "} {
		t.Run(key, func(t *testing.T) {
			m := testModel(t)
			m.done = true

			_, cmd := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(key)}))
			if cmd == nil {
				t.Fatalf("%q on the report did not quit", key)
			}
		})
	}
}

func TestSetupQuitKeys(t *testing.T) {
	keys := map[string]tea.Key{
		"q":      {Type: tea.KeyRunes, Runes: []rune("q")},
		"esc":    {Type: tea.KeyEsc},
		"ctrl+c": {Type: tea.KeyCtrlC},
	}

	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			settings := &Settings{Mode: string(ModeDark), Palette: Palettes()[0].Name}

			m := &setupModel{settings: settings, text: NewText(LangEN)}
			m.restyle()

			_, cmd := m.Update(tea.KeyMsg(key))
			if cmd == nil {
				t.Fatalf("%q did not leave the setup wizard", name)
			}
		})
	}
}

func TestHelpListsTabAndQuit(t *testing.T) {
	m := testModel(t)
	help := m.help()

	for _, want := range []string{"tab", m.text.HelpQuit()} {
		if !strings.Contains(help, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}

func TestHelpOffersNoSecondQuit(t *testing.T) {
	// One q leaves now; a line about pressing it again describes a stop that
	// no longer exists.
	m := testModel(t)
	if strings.Contains(m.help(), m.text.HelpQuitAgain()) {
		t.Error("help still tells to press q again")
	}
}

func TestFooterSaysQLeaves(t *testing.T) {
	m := testModel(t)
	if footer := m.footer(); !strings.Contains(footer, "q quit") {
		t.Errorf("footer = %q, want q described as quitting, not stopping", footer)
	}
}

func TestSparklineGapsInsteadOfZeroes(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	withGap := sparkline(s, []float64{10, math.NaN(), 12}, 3)
	if !strings.Contains(withGap, "·") {
		t.Errorf("a tick with no measurement must be a gap, got %q", withGap)
	}

	// The gap must stay out of the scale: otherwise a NaN read as zero would
	// stretch the range and flatten the real values.
	scaled := sparkline(s, []float64{10, math.NaN(), 11}, 3)
	flat := sparkline(s, []float64{10, 11}, 2)

	if !strings.ContainsAny(scaled, string(sparkLevels)) || !strings.ContainsAny(flat, string(sparkLevels)) {
		t.Errorf("both lines must still draw levels: %q and %q", scaled, flat)
	}

	if got := lipglossWidth(sparkline(s, []float64{math.NaN(), math.NaN()}, 20)); got != 20 {
		t.Errorf("all-gap sparkline width = %d, want 20", got)
	}
}
