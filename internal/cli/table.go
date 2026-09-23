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

	"github.com/charmbracelet/lipgloss"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// The final screen's table has the text report's seven columns: sent, failed,
// sent/s and four latencies. The first three are the counts.
const (
	tableColumns = 7
	countColumns = 3
	// minNameWidth is the least a name keeps on the row of its numbers; below
	// it the name takes a line of its own.
	minNameWidth = 8
)

// columnFloors are the least each column takes, so ordinary numbers keep the
// table in one line per method from 64 columns in English up.
var columnFloors = [tableColumns]int{5, 6, 6, 6, 6, 6, 6}

type tableRow struct {
	label string
	// sub marks a refused or rejected row under its method.
	sub   bool
	cells [tableColumns]string
	bad   bool
}

func screenRows(report engine.Report) []tableRow {
	var rows []tableRow
	for i := range report.Methods {
		m := &report.Methods[i]
		rows = append(rows, tableRow{
			label: shortMethod(m.Method),
			cells: [tableColumns]string{countCell(m.Sent), countCell(m.Failed), fmt.Sprintf("%.0f", m.RPS),
				formatQuantile(m.P50), formatQuantile(m.P90), formatQuantile(m.P95), formatQuantile(m.P99)},
			bad: m.Failed > 0,
		})
		for _, sub := range []struct {
			label string
			r     engine.RefusalLatency
		}{{"rejected", m.Rejected}, {"refused", m.Refusal}} {
			if sub.r.Count == 0 {
				continue
			}
			rows = append(rows, tableRow{
				label: sub.label, sub: true,
				cells: [tableColumns]string{"", countCell(sub.r.Count), "",
					formatQuantile(sub.r.P50), formatQuantile(sub.r.P90), formatQuantile(sub.r.P95), formatQuantile(sub.r.P99)},
			})
		}
	}

	return rows
}

func countCell(n int) string {
	if n < 0 {
		return fmt.Sprint(n)
	}

	return compactCount(uint64(n))
}

// finalTable lays the table out in one of three ways, the same for every row:
// the name and the seven numbers on one line; the name on a line of its own
// and the numbers under it; or the name, the counts and the latencies on a
// line each. Columns are as wide as their widest cell over the whole table.
func (m *model) finalTable(width int) string {
	rows := screenRows(m.report)
	header := [tableColumns]string{m.text.SentColumn(), m.text.Errors(), "sent/s", "p50", "p90", "p95", "p99"}

	var widths [tableColumns]int
	for i := range widths {
		widths[i] = max(columnFloors[i], lipgloss.Width(header[i]))
		for _, row := range rows {
			widths[i] = max(widths[i], lipgloss.Width(row.cells[i]))
		}
	}

	numbers := 0
	for _, w := range widths {
		numbers += 1 + w
	}

	var b strings.Builder
	line := func(style lipgloss.Style, text string) {
		b.WriteString(style.Render(text))
		b.WriteString("\n")
	}
	cells := func(c [tableColumns]string, from, to int) string {
		parts := make([]string, 0, to-from)
		for i := from; i < to; i++ {
			parts = append(parts, padLeft(c[i], widths[i]))
		}

		return strings.Join(parts, " ")
	}

	if nameWidth := width - numbers; nameWidth >= minNameWidth {
		longest := lipgloss.Width(m.text.ColumnMethod())
		for _, row := range rows {
			longest = max(longest, lipgloss.Width(rowLabel(row, width)))
		}
		nameWidth = min(nameWidth, longest)

		line(m.styles.label, padRight(m.text.ColumnMethod(), nameWidth)+" "+cells(header, 0, tableColumns))
		for _, row := range rows {
			b.WriteString(m.styles.value.Render(padRight(rowLabel(row, nameWidth), nameWidth) + " "))
			line(m.rowStyle(row), cells(row.cells, 0, tableColumns))
		}

		return b.String()
	}

	split := numbers-1 > width
	line(m.styles.label, m.text.ColumnMethod())
	if split {
		line(m.styles.label, cells(header, 0, countColumns))
		line(m.styles.label, cells(header, countColumns, tableColumns))
	} else {
		line(m.styles.label, cells(header, 0, tableColumns))
	}
	for _, row := range rows {
		line(m.styles.value, rowLabel(row, width))
		if split {
			line(m.rowStyle(row), cells(row.cells, 0, countColumns))
			line(m.rowStyle(row), cells(row.cells, countColumns, tableColumns))
		} else {
			line(m.rowStyle(row), cells(row.cells, 0, tableColumns))
		}
	}

	return b.String()
}

// rowLabel fits a row's name into width, cutting a method from its head:
// methods of one service differ in their tail.
func rowLabel(row tableRow, width int) string {
	if row.sub && lipgloss.Width(row.label)+2 <= width {
		return "  " + row.label
	}

	return truncateLeft(row.label, width)
}

func (m *model) rowStyle(row tableRow) lipgloss.Style {
	if row.bad {
		return m.styles.bad
	}

	return m.styles.value
}
