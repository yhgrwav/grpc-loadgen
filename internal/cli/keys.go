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
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	// hintHold is how long the unknown-key hint stays at full brightness, and
	// hintFade how long it then takes to fade out.
	hintHold = 1500 * time.Millisecond
	hintFade = 500 * time.Millisecond
)

// russianKeys maps the letters of the Russian ЙЦУКЕН layout to the Latin
// letters on the same keys. Russian is an interface language, so its layout
// must drive the view without switching. German QWERTZ keeps every command
// letter in place, and Chinese is typed through an IME over a Latin layout;
// other layouts get the unknown-key hint.
var russianKeys = map[rune]rune{
	'й': 'q', 'ц': 'w', 'у': 'e', 'к': 'r', 'е': 't', 'н': 'y', 'г': 'u', 'ш': 'i', 'щ': 'o', 'з': 'p',
	'ф': 'a', 'ы': 's', 'в': 'd', 'а': 'f', 'п': 'g', 'р': 'h', 'о': 'j', 'л': 'k', 'д': 'l',
	'я': 'z', 'ч': 'x', 'с': 'c', 'м': 'v', 'и': 'b', 'т': 'n', 'ь': 'm',
}

// keyOf is the key as the view's commands name it.
func keyOf(msg tea.KeyMsg) string {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		if latin, ok := russianKeys[unicode.ToLower(msg.Runes[0])]; ok {
			return string(latin)
		}
	}

	return msg.String()
}

// unknownKey starts the hint for a key that is no command. Only typed
// characters get it: a stray arrow or function key says nothing about the
// layout.
func (m *model) unknownKey(msg tea.KeyMsg) {
	if msg.Type != tea.KeyRunes {
		return
	}

	m.hintKey = string(msg.Runes)
	m.hintAt = m.frame
}

// hintStage is the brightness step of the unknown-key hint: 0 is full, 1 and
// 2 fade, -1 means no hint.
func (m *model) hintStage() int {
	if m.hintKey == "" {
		return -1
	}

	age := time.Duration(m.frame-m.hintAt) * refresh

	switch {
	case age < hintHold:
		return 0
	case age < hintHold+hintFade/2:
		return 1
	case age < hintHold+hintFade:
		return 2
	default:
		return -1
	}
}
