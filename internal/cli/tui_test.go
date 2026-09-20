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
	settings.Theme = "ember"

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
	if again.Lang != string(LangRU) || again.Theme != "ember" {
		t.Errorf("reloaded %q/%q, want ru/ember", again.Lang, again.Theme)
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

func TestThemeByNameFallsBack(t *testing.T) {
	if got := ThemeByName("nope").Name; got != Themes()[0].Name {
		t.Errorf("unknown theme resolved to %q, want the first one", got)
	}
	if got := ThemeByName("ember").Name; got != "ember" {
		t.Errorf("ThemeByName(ember) = %q", got)
	}
}

func TestSparklineFitsWidth(t *testing.T) {
	s := newStyles(ThemeByName("mono"))

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
	s := newStyles(ThemeByName("mono"))

	if got := lipglossWidth(gauge(s, 500, 100, 10)); got != 10 {
		t.Errorf("gauge width with an over-limit value = %d, want 10", got)
	}
	if got := lipglossWidth(gauge(s, -5, 100, 10)); got != 10 {
		t.Errorf("gauge width with a negative value = %d, want 10", got)
	}
}

func TestProgressAtEdges(t *testing.T) {
	s := newStyles(ThemeByName("mono"))

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
