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

// Ground: contract — the statement is about the target, so its denominator is
// what the target could have seen. 150 after warmup, 4 never went out, the
// other 146 got no answer: "146 of 146", and the 4 named in the same block.
func TestPrintReportStatesSilenceOverTheCallsThatWentOut(t *testing.T) {
	stats := engine.NewStats()
	stats.Reserve(3*time.Second, "a.B/One")
	start := time.Now()
	stats.Start(start, 0)
	for i := range 150 {
		at := start.Add(time.Duration(i) * 10 * time.Millisecond)
		stats.Record(engine.Result{
			Method: "a.B/One", ScheduledAt: at, BegunAt: at, Deadline: at.Add(300 * time.Millisecond),
			Outcome: engine.Outcome{Category: engine.CategoryTimeout, NotSent: i < 4, SentAt: at,
				DoneAt: at.Add(300 * time.Millisecond)},
		})
	}
	stats.Finish(start.Add(2 * time.Second))

	// The engine fills in the plan; the counts are the stats' own.
	report := stats.Report()
	report.Methods[0].RPSLow, report.Methods[0].RPSHigh = 100, 100
	report.Methods[0].Timeout = 300 * time.Millisecond

	var out strings.Builder
	PrintReport(&out, "localhost:50051", report)
	text := out.String()

	if !strings.Contains(text, "146 of 146 calls (100.0%) got no answer") {
		t.Errorf("the statement does not count over the 146 calls that went out:\n%s", text)
	}
	if !strings.Contains(text, "and the target answered nothing at all.\na.B/One: 4 calls timed out before going out") {
		t.Errorf("the 4 unsent calls are not in the same block:\n%s", text)
	}
}
