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
	"time"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{in: 0, want: "0"},
		{in: 900 * time.Nanosecond, want: "900ns"},
		{in: 250 * time.Microsecond, want: "250us"},
		{in: 42 * time.Millisecond, want: "42ms"},
		{in: 3500 * time.Millisecond, want: "3.5s"},
		{in: 95 * time.Second, want: "1m35s"},
	}

	for _, tt := range tests {
		if got := formatDuration(tt.in); got != tt.want {
			t.Errorf("formatDuration(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPrintReport(t *testing.T) {
	report := engine.Report{
		Duration: 4 * time.Second,
		Sent:     1000,
		Failed:   22,
		Methods: []engine.MethodReport{
			{Method: "a.B/One", Sent: 800, Failed: 20, RPS: 200, P50: 31 * time.Millisecond, P99: 36 * time.Millisecond},
		},
	}

	var out strings.Builder
	PrintReport(&out, "localhost:50051", report)

	text := out.String()

	for _, want := range []string{"localhost:50051", "sent 1000, failed 22", "a.B/One", "31ms", "36ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("report does not mention %q:\n%s", want, text)
		}
	}
}
