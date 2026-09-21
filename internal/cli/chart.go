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
	"strings"
	"time"
)

// point is one tick of latency history, in milliseconds. NaN means nothing was
// measured; a bound flag means the value is a lower bound, not the percentile.
type point struct {
	p50, p90, p99             float64
	bound50, bound90, bound99 bool
}

// latencySeries is one row of the latency block.
type latencySeries struct {
	label  string
	values []float64
	bounds []bool
}

func latencySeriesOf(points []point) []latencySeries {
	series := []latencySeries{{label: "p50"}, {label: "p90"}, {label: "p99"}}

	for _, p := range points {
		series[0].values = append(series[0].values, p.p50)
		series[0].bounds = append(series[0].bounds, p.bound50)
		series[1].values = append(series[1].values, p.p90)
		series[1].bounds = append(series[1].bounds, p.bound90)
		series[2].values = append(series[2].values, p.p99)
		series[2].bounds = append(series[2].bounds, p.bound99)
	}

	return series
}

// latencyScale is the range every latency row is drawn on: from the lowest
// known value to the highest across all rows, so one height means one value
// in any row. ok is false when nothing was measured.
func latencyScale(series []latencySeries) (low, high float64, ok bool) {
	for _, s := range series {
		for _, v := range s.values {
			if math.IsNaN(v) {
				continue
			}
			if !ok {
				low, high, ok = v, v, true

				continue
			}

			low = min(low, v)
			high = max(high, v)
		}
	}

	return low, high, ok
}

// latencyCells draws values on the shared scale, eight levels a cell. A gap
// stands where nothing was measured; a bound is drawn in the warning colour.
func latencyCells(s styles, values []float64, bounds []bool, low, high float64, width int) string {
	if len(values) > width {
		values = values[len(values)-width:]
		bounds = bounds[len(bounds)-width:]
	}

	var b strings.Builder

	b.WriteString(s.pad(width - len(values)))

	span := high - low

	for i, v := range values {
		if math.IsNaN(v) {
			b.WriteString(s.faint.Render("·"))

			continue
		}

		level := len(sparkLevels) / 2
		if span > 0 {
			level = int((v - low) / span * float64(len(sparkLevels)-1))
		}
		level = min(max(level, 0), len(sparkLevels)-1)

		style := s.spark
		if bounds[i] {
			style = s.warn
		}
		b.WriteString(style.Render(string(sparkLevels[level])))
	}

	return b.String()
}

// latencyValue is the latest value of a row as the report writes it.
func latencyValue(values []float64, bounds []bool) string {
	if len(values) == 0 || math.IsNaN(values[len(values)-1]) {
		return "-"
	}

	text := formatDuration(millis(values[len(values)-1]))
	if bounds[len(bounds)-1] {
		return ">" + text
	}

	return text
}

func millis(v float64) time.Duration {
	return time.Duration(v * float64(time.Millisecond))
}
