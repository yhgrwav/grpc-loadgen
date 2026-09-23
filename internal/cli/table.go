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
// It returns the heading's lines and, per method, the lines of its rows.
func (m *model) finalTable(width int) (head []string, groups [][]string) {
	rows := screenRows(m.report)
	header := [tableColumns]string{m.text.SentColumn(), m.text.Failed(), "sent/s", "p50", "p90", "p95", "p99"}

	var widths [tableColumns]int
	for i := range widths {
		widths[i] = max(columnFloors[i], lipgloss.Width(header[i]))
		for j := range rows {
			widths[i] = max(widths[i], lipgloss.Width(rows[j].cells[i]))
		}
	}

	numbers := 0
	for _, w := range widths {
		numbers += 1 + w
	}

	cells := func(c [tableColumns]string, from, to int) string {
		parts := make([]string, 0, to-from)
		for i := from; i < to; i++ {
			parts = append(parts, padLeft(c[i], widths[i]))
		}

		return strings.Join(parts, " ")
	}
	add := func(row *tableRow, lines ...string) {
		if !row.sub || len(groups) == 0 {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], lines...)
	}

	if nameWidth := width - numbers; nameWidth >= minNameWidth {
		longest := lipgloss.Width(m.text.ColumnMethod())
		for i := range rows {
			row := &rows[i]
			longest = max(longest, lipgloss.Width(rowLabel(row, width)))
		}
		nameWidth = min(nameWidth, longest)

		head = append(head, m.styles.label.Render(padRight(m.text.ColumnMethod(), nameWidth)+" "+cells(header, 0, tableColumns)))
		for i := range rows {
			row := &rows[i]
			add(row, m.styles.value.Render(padRight(rowLabel(row, nameWidth), nameWidth)+" ")+
				m.rowStyle(row).Render(cells(row.cells, 0, tableColumns)))
		}

		return head, groups
	}

	split := numbers-1 > width
	head = append(head, m.styles.label.Render(m.text.ColumnMethod()))
	if split {
		head = append(head, m.styles.label.Render(cells(header, 0, countColumns)),
			m.styles.label.Render(cells(header, countColumns, tableColumns)))
	} else {
		head = append(head, m.styles.label.Render(cells(header, 0, tableColumns)))
	}
	for i := range rows {
		row := &rows[i]
		if split {
			add(row, m.styles.value.Render(rowLabel(row, width)),
				m.rowStyle(row).Render(cells(row.cells, 0, countColumns)),
				m.rowStyle(row).Render(cells(row.cells, countColumns, tableColumns)))
		} else {
			add(row, m.styles.value.Render(rowLabel(row, width)),
				m.rowStyle(row).Render(cells(row.cells, 0, tableColumns)))
		}
	}

	return head, groups
}

// rowLabel fits a row's name into width, cutting a method from its head:
// methods of one service differ in their tail.
func rowLabel(row *tableRow, width int) string {
	if row.sub && lipgloss.Width(row.label)+2 <= width {
		return "  " + row.label
	}

	return truncateLeft(row.label, width)
}

func (m *model) rowStyle(row *tableRow) lipgloss.Style {
	if row.bad {
		return m.styles.bad
	}

	return m.styles.value
}
