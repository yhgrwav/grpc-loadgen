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
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			"summary":  text.Summary(),
			"running":  text.Running(),
			"sent":     text.Sent(),
			"errors":   text.Errors(),
			"inflight": text.InFlight(),
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

func TestChartPlotsMedianAndSpread(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	points := []point{
		{p50: 10, p90: 20, p99: 30},
		{p50: 12, p90: 25, p99: 60},
		{p50: 11, p90: 22, p99: 45},
	}

	rows := chart(s, points, 12, 7)

	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(rows))
	}

	joined := strings.Join(rows, "")
	if !strings.Contains(joined, "●") {
		t.Error("the chart does not plot the median")
	}
	if !strings.Contains(joined, "·") {
		t.Error("the chart does not plot the spread")
	}

	for i, row := range rows {
		if got := lipglossWidth(row); got != 12 {
			t.Errorf("row %d width = %d, want 12", i, got)
		}
	}
}

func TestChartOfNothingIsBlank(t *testing.T) {
	s := newStyles(ThemeFor("mono", ModeDark))

	rows := chart(s, nil, 10, 5)

	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	for _, row := range rows {
		if got := lipglossWidth(row); got != 10 {
			t.Errorf("row width = %d, want 10", got)
		}
	}
}
