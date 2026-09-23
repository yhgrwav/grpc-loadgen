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
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
)

// Ground: contract — one report, two renderings: the methods nothing could be
// checked against travel in the run's report, so the screen names them as the
// log does.
func TestFinalScreenNamesTheMethodsNotChecked(t *testing.T) {
	report := RunReport{
		Report:    engine.Report{Sent: 10, Methods: []engine.MethodReport{{Method: "a.B/One", Sent: 10}}},
		Unchecked: []Unchecked{{Method: "a.B/One", Err: descriptor.ErrReflectionUnsupported}},
	}

	var text strings.Builder
	PrintReport(&text, "localhost:50051", report)

	m := testModel(t)
	m.reportOf = func() RunReport { return report }
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(doneMsg{})
	screen := m.finalReport(contentWidth(120))

	for _, out := range []string{text.String(), screen} {
		if !strings.Contains(out, "not checked before the run") || !strings.Contains(out, "reflection is off") {
			t.Errorf("the unchecked method is not named:\n%s", out)
		}
	}
}
