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
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// stopping builds a model that has had the stop key pressed the given number
// of times and holds inFlight calls waiting for an answer.
func stopping(t *testing.T, presses, inFlight int) *model {
	t.Helper()

	m := testModel(t)
	m.snapshot.InFlight = inFlight

	for range presses {
		m.stopper.Press()
	}

	return m
}

func TestRunningHeaderHasNoStopHint(t *testing.T) {
	m := stopping(t, 0, 12)

	line := firstLine(m.header(120))
	for _, hint := range []string{m.text.StopAgainAborts(), m.text.StopAgainExits()} {
		if strings.Contains(line, hint) {
			t.Errorf("header %q offers %q while nobody asked to stop", line, hint)
		}
	}
}

func TestGentleStopShowsInFlightAndOffersTheAbort(t *testing.T) {
	m := stopping(t, 1, 12)

	line := firstLine(m.header(120))
	if !strings.Contains(line, m.text.InFlightCount("12")) {
		t.Errorf("header %q does not say how many calls the drain is waiting for", line)
	}
	if !strings.Contains(line, m.text.StopAgainAborts()) {
		t.Errorf("header %q does not say that another q cuts the calls off", line)
	}
	if strings.Contains(line, m.text.StopAgainExits()) {
		t.Errorf("header %q offers the exit without a report before the abort", line)
	}
}

func TestStopHintCountFollowsTheSnapshot(t *testing.T) {
	m := stopping(t, 1, 12)
	m.header(120)

	m.snapshot.InFlight = 7

	line := firstLine(m.header(120))
	if !strings.Contains(line, m.text.InFlightCount("7")) {
		t.Errorf("header %q kept the count of the moment q was pressed", line)
	}
	if strings.Contains(line, m.text.InFlightCount("12")) {
		t.Errorf("header %q still shows the old count", line)
	}
}

func TestGentleStopWithNothingInFlightOffersNoAbort(t *testing.T) {
	m := stopping(t, 1, 0)

	line := firstLine(m.header(120))
	if strings.Contains(line, m.text.StopAgainAborts()) {
		t.Errorf("header %q offers to cut off calls while none is in flight", line)
	}
	if strings.Contains(line, m.text.InFlightCount("0")) {
		t.Errorf("header %q counts an empty drain", line)
	}
	if !strings.Contains(line, m.text.Stopping()) {
		t.Errorf("header %q dropped the status itself", line)
	}
}

func TestAbortOffersTheExitNotAnotherAbort(t *testing.T) {
	for name, start := range map[string]func(*model){
		"two presses": func(m *model) { m.stopper.Press(); m.stopper.Press() },
		"sigterm":     func(m *model) { m.stopper.Abort() },
	} {
		t.Run(name, func(t *testing.T) {
			m := stopping(t, 0, 5)
			start(m)

			line := firstLine(m.header(120))
			if !strings.Contains(line, m.text.StopAgainExits()) {
				t.Errorf("header %q does not offer the way out of an abort that hangs", line)
			}
			if strings.Contains(line, m.text.StopAgainAborts()) {
				t.Errorf("header %q promises an abort that is already under way", line)
			}
			// The abort is cutting those calls off while the line is read, so
			// the count would be stale; the way out takes the room instead.
			if strings.Contains(line, m.text.InFlightCount("5")) {
				t.Errorf("header %q counts calls the abort is already cutting off", line)
			}
		})
	}
}

func TestAbortWithNothingInFlightStillOffersTheExit(t *testing.T) {
	m := stopping(t, 2, 0)

	if line := firstLine(m.header(120)); !strings.Contains(line, m.text.StopAgainExits()) {
		t.Errorf("header %q hides the way out: the report itself can hang", line)
	}
}

func TestFinishedRunHasNoStopHint(t *testing.T) {
	m := stopping(t, 1, 5)
	m.done = true

	line := firstLine(m.header(120))
	for _, hint := range []string{m.text.StopAgainAborts(), m.text.StopAgainExits()} {
		if strings.Contains(line, hint) {
			t.Errorf("header %q still offers %q after the run is over", line, hint)
		}
	}
}

func TestStopHintFitsTheNarrowestView(t *testing.T) {
	width := contentWidth(72) // the floor viewWidth falls back to

	for _, lang := range Languages() {
		for _, presses := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", lang.Lang, presses), func(t *testing.T) {
				m := stopping(t, presses, 1234)
				m.settings.Lang = string(lang.Lang)
				m.applySettings()
				m.service = "WalletService"
				m.target = "some-really-long-host.example.internal:50051"

				line := firstLine(m.header(width))
				if got := lipgloss.Width(line); got > width {
					t.Errorf("header is %d columns wide in %s at width %d: the frame wraps it",
						got, lang.Lang, width)
				}

				hint := m.text.StopAgainAborts()
				if presses > 1 {
					hint = m.text.StopAgainExits()
				}
				if !strings.Contains(line, hint) {
					t.Errorf("header %q dropped the stop hint to fit the address", line)
				}
			})
		}
	}
}

// A terminal too narrow for both keeps the part that says what to press: a cut
// «1234 в полёте …» tells the user nothing they can act on.
func TestNarrowHeaderKeepsTheKeyOverTheCount(t *testing.T) {
	const width = 45 // a 53-column terminal, too narrow for the count and the key

	m := stopping(t, 1, 1234)
	m.settings.Lang = string(LangRU)
	m.applySettings()

	line := firstLine(m.header(width))
	if !strings.Contains(line, m.text.StopAgainAborts()) {
		t.Errorf("header %q cut the key that stops the calls", line)
	}
	if strings.Contains(line, m.text.InFlightCount("1234")) {
		t.Errorf("header %q kept the count and had to cut the key", line)
	}
	if got := lipgloss.Width(line); got > width {
		t.Errorf("header is %d columns wide at width %d", got, width)
	}
}

// The pieces the line cannot show are dropped whole: a «W…» where the service
// name belongs reads as a broken layout, not as a shortened name.
func TestHeaderDropsAPieceItCannotShow(t *testing.T) {
	m := stopping(t, 1, 1234)
	m.settings.Lang = string(LangRU)
	m.applySettings()
	m.service = "WalletService"
	m.target = "wallet.prod.internal:50051"

	if line := firstLine(m.header(contentWidth(72))); strings.Contains(line, "W…") {
		t.Errorf("header %q shows a shard of the service name", line)
	}
}

func TestStopHintsTranslated(t *testing.T) {
	en := NewText(LangEN)

	for _, lang := range Languages() {
		text := NewText(lang.Lang)

		for name, pair := range map[string][2]string{
			"StopAgainAborts": {text.StopAgainAborts(), en.StopAgainAborts()},
			"StopAgainExits":  {text.StopAgainExits(), en.StopAgainExits()},
			"InFlightCount":   {text.InFlightCount("7"), en.InFlightCount("7")},
		} {
			if pair[0] == "" {
				t.Errorf("%s is empty in %s", name, lang.Lang)
			}
			if lang.Lang != LangEN && pair[0] == pair[1] {
				t.Errorf("%s in %s is the English string: a missing translation", name, lang.Lang)
			}
		}
	}
}
