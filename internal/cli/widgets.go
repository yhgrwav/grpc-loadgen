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
	"time"
)

var (
	spinnerFrames = []string{"◜", "◝", "◞", "◟"}
	sparkLevels   = []rune("▁▂▃▄▅▆▇█")
)

func spinner(frame int) string {
	return spinnerFrames[frame%len(spinnerFrames)]
}

func gauge(s styles, value, limit float64, width int) string {
	filled := 0
	if limit > 0 {
		filled = int(float64(width) * value / limit)
	}
	filled = min(max(filled, 0), width)

	return s.barOn.Render(strings.Repeat("█", filled)) +
		s.barOff.Render(strings.Repeat("░", width-filled))
}

func progress(s styles, elapsed, total time.Duration, width int) string {
	filled := 0
	if total > 0 {
		filled = int(float64(width) * float64(elapsed) / float64(total))
	}
	filled = min(max(filled, 0), width)

	bar := s.barOn.Render(strings.Repeat("━", filled))
	if filled < width {
		bar += s.barOff.Render(strings.Repeat("━", width-filled))
	}

	return bar
}

func sparkline(s styles, values []float64, width int) string {
	if len(values) == 0 {
		return s.faint.Render(strings.Repeat("·", width))
	}

	if len(values) > width {
		values = values[len(values)-width:]
	}

	peak := 0.0
	for _, v := range values {
		peak = max(peak, v)
	}

	var b strings.Builder

	if pad := width - len(values); pad > 0 {
		b.WriteString(s.faint.Render(strings.Repeat("·", pad)))
	}

	for _, v := range values {
		level := 0
		if peak > 0 {
			level = int(v / peak * float64(len(sparkLevels)-1))
		}
		level = min(max(level, 0), len(sparkLevels)-1)
		b.WriteRune(sparkLevels[level])
	}

	return s.spark.Render(b.String())
}

func statLine(s styles, pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))

	for _, pair := range pairs {
		parts = append(parts, s.label.Render(pair[0]+" ")+s.value.Render(pair[1]))
	}

	return strings.Join(parts, s.faint.Render("  ·  "))
}

func keyHint(s styles, hints ...string) string {
	parts := make([]string, 0, len(hints))

	for _, hint := range hints {
		parts = append(parts, s.faint.Render(hint))
	}

	return strings.Join(parts, s.faint.Render("   "))
}

func formatCount(n int) string {
	text := fmt.Sprintf("%d", n)
	if len(text) <= 4 {
		return text
	}

	var b strings.Builder

	for i, r := range text {
		if i > 0 && (len(text)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}

	return b.String()
}
