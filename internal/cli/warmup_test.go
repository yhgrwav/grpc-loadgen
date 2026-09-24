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

	"github.com/yhgrwav/leettest/pkg/engine"
)

func printWarmup(warmup time.Duration, sent, failed int) string {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: 30 * time.Second, Warmup: warmup, WarmupSent: sent, WarmupFailed: failed,
		Sent: 1250, Methods: []engine.MethodReport{{Method: "a.B/One", Sent: 1250, RPS: 50}},
	}})

	return out.String()
}

// The header's sent leaves the warmup out; the line next to it says how many
// went out then, so sent plus it is what the target saw (ledger: 1250 + 250).
func TestPrintReport_SaysHowManyWarmupCallsWentOut(t *testing.T) {
	text := printWarmup(5*time.Second, 250, 0)

	if !strings.Contains(text, "warm-up 250 sent, excluded from stats") {
		t.Errorf("no warm-up line:\n%s", text)
	}
	if strings.Contains(text, "failed)") {
		t.Errorf("a failed count in brackets with nothing failed:\n%s", text)
	}
}

// Failures during the warmup are a problem of the start, shown, not hidden.
func TestPrintReport_ShowsWarmupFailures(t *testing.T) {
	if text := printWarmup(5*time.Second, 250, 12); !strings.Contains(text, "warm-up 250 sent (12 failed), excluded from stats") {
		t.Errorf("warm-up failures not shown:\n%s", text)
	}
}

// No warmup, no line; a warmup stopped before a call went out still has one.
func TestPrintReport_WarmupLineOnlyWithAWarmup(t *testing.T) {
	if text := printWarmup(0, 0, 0); strings.Contains(text, "warm-up") {
		t.Errorf("a warm-up line with no warmup:\n%s", text)
	}
	if text := printWarmup(5*time.Second, 0, 0); !strings.Contains(text, "warm-up 0 sent, excluded from stats") {
		t.Errorf("no warm-up line for a warmup that sent nothing:\n%s", text)
	}
}

// The plain live line during the warmup shows the calls going out, not sent 0.
func TestPlainLine_ShowsWarmupCallsGoingOut(t *testing.T) {
	line := plainLine(engine.Snapshot{Elapsed: 2 * time.Second, Warmup: 5 * time.Second, WarmupSent: 120})

	if !strings.Contains(line, "warming up: 120 sent") {
		t.Errorf("line = %q, want it to show the warm-up calls", line)
	}
	if strings.Contains(line, "sent 0") {
		t.Errorf("line = %q: sent 0 while calls go out", line)
	}
	if after := plainLine(engine.Snapshot{Elapsed: 6 * time.Second, Warmup: 5 * time.Second, Sent: 50, WarmupSent: 250}); strings.Contains(after, "warming up") {
		t.Errorf("after the warmup, line = %q", after)
	}
}

// The live view's warm-up note carries the count too.
func TestLiveNote_ShowsWarmupCallsGoingOut(t *testing.T) {
	m := &model{warmup: 5 * time.Second, text: Text{lang: LangEN}}
	m.snapshot = engine.Snapshot{Elapsed: 2 * time.Second, WarmupSent: 120}

	full, short := m.note()
	for _, n := range []string{full, short} {
		if !strings.Contains(n, "120 sent") {
			t.Errorf("note %q does not show the warm-up calls", n)
		}
	}
}
