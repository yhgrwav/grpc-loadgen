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
)

type point struct {
	p50 float64
	p90 float64
	p99 float64
}

func chart(s styles, points []point, width, height int) []string {
	if height < 3 {
		height = 3
	}

	rows := make([]string, height)

	if len(points) == 0 {
		for i := range rows {
			rows[i] = s.faint.Render(strings.Repeat("·", width))
		}

		return rows
	}

	if len(points) > width {
		points = points[len(points)-width:]
	}

	low, high := points[0].p50, points[0].p99
	for _, p := range points {
		low = min(low, p.p50)
		high = max(high, p.p99)
	}

	if high-low < 1 {
		high = low + 1
	}

	grid := make([][]rune, height)
	marks := make([][]bool, height)

	for i := range grid {
		grid[i] = []rune(strings.Repeat(" ", len(points)))
		marks[i] = make([]bool, len(points))
	}

	rowOf := func(v float64) int {
		ratio := (v - low) / (high - low)
		row := height - 1 - int(ratio*float64(height-1))

		return min(max(row, 0), height-1)
	}

	for x, p := range points {
		grid[rowOf(p.p99)][x] = '·'
		grid[rowOf(p.p90)][x] = '·'

		row := rowOf(p.p50)
		grid[row][x] = '●'
		marks[row][x] = true
	}

	for y := range grid {
		var b strings.Builder

		for x, r := range grid[y] {
			switch {
			case marks[y][x]:
				b.WriteString(s.spark.Render(string(r)))
			case r == ' ':
				b.WriteString(s.pad(1))
			default:
				b.WriteString(s.faint.Render(string(r)))
			}
		}

		if pad := width - len(points); pad > 0 {
			rows[y] = s.pad(pad) + b.String()

			continue
		}

		rows[y] = b.String()
	}

	return rows
}

func chartScale(s styles, points []point, height int) []string {
	labels := make([]string, height)
	for i := range labels {
		labels[i] = s.pad(8)
	}

	if len(points) == 0 {
		return labels
	}

	low, high := points[0].p50, points[0].p99
	for _, p := range points {
		low = min(low, p.p50)
		high = max(high, p.p99)
	}

	if high-low < 1 {
		high = low + 1
	}

	labels[0] = s.faint.Render(fmt.Sprintf("%6.0fms", high))
	labels[height-1] = s.faint.Render(fmt.Sprintf("%6.0fms", low))

	return labels
}
