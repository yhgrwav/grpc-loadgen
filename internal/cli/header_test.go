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
	"github.com/charmbracelet/lipgloss"
)

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

func TestRunningStatusSaysWhatIsHappening(t *testing.T) {
	if got := NewText(LangRU).Running(); got != "выполняется нагрузочное тестирование" {
		t.Errorf("Running() = %q", got)
	}
}

func TestHeaderReadsStatusServiceTarget(t *testing.T) {
	m := testModel(t)
	m.service = "WalletService"

	line := firstLine(m.header(120))

	status := strings.Index(line, m.text.Running())
	service := strings.Index(line, "WalletService")
	target := strings.Index(line, "localhost:50051")

	if status < 0 || service < 0 || target < 0 {
		t.Fatalf("header %q lacks status, service or target", line)
	}
	if status >= service || service >= target {
		t.Errorf("header %q is not in the order status · service · target", line)
	}
}

func TestHeaderSpinnerIsBraille(t *testing.T) {
	m := testModel(t)

	if line := firstLine(m.header(120)); !strings.ContainsAny(line, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
		t.Errorf("header %q has no braille spinner", line)
	}
}

func TestProjectNameMovesToTheFooter(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if line := firstLine(m.header(contentWidth(120))); strings.Contains(line, "grpc-loadgen") {
		t.Errorf("header %q still leads with the project name", line)
	}
	if footer := m.footer(); !strings.Contains(footer, "grpc-loadgen") {
		t.Errorf("footer %q does not carry the project name", footer)
	}
}

// oldSparkWidth is the fixed width sparklines had before they followed the
// terminal: the view looked right only at half a screen.
const oldSparkWidth = 48

func TestWideTerminalGetsLongerSparklines(t *testing.T) {
	narrow, wide := testModel(t), testModel(t)
	narrow.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	wide.Update(tea.WindowSizeMsg{Width: 200, Height: 40})

	if n, w := narrow.sparkCells(), wide.sparkCells(); w <= n || w <= oldSparkWidth {
		t.Errorf("cells: %d at 80 columns, %d at 200, want the wide terminal to use its width past %d",
			n, w, oldSparkWidth)
	}
}

func TestWideTerminalFillsTheLatencyRow(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})

	h := &history{}
	for range historyLimit {
		h.push(1, exact(10), exact(20), exact(30))
	}

	row := latencyLine(t, m.latencyChart(h.points), "p50")
	if w := lipgloss.Width(row); w < contentWidth(200)-sparkValueWidth {
		t.Errorf("latency row is %d wide on a 200-column terminal, leaving most of it empty", w)
	}
}
