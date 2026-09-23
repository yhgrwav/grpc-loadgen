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
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// Ground: contract — calls that never went out leave "sent" and "failed", so
// the totals name them, or 4 calls of the schedule vanish from the report.
func TestPrintReportNamesTheCallsThatDidNotGoOut(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", engine.Report{Sent: 146, Failed: 146, NotSent: 4})

	if !strings.Contains(out.String(), "sent 146, failed 146, not sent 4\n") {
		t.Errorf("the totals do not name the 4 calls that did not go out:\n%s", out.String())
	}

	out.Reset()
	PrintReport(&out, "localhost:50051", engine.Report{Sent: 146, Failed: 146})
	if strings.Contains(out.String(), "not sent") {
		t.Errorf("nothing was left unsent, yet the totals say so:\n%s", out.String())
	}
}

// Ground: contract — the live view and the final screen show the same totals
// as the text report, in every language.
func TestScreensNameTheCallsThatDidNotGoOut(t *testing.T) {
	for _, lang := range allLangs {
		t.Run(string(lang), func(t *testing.T) {
			m := testModel(t)
			m.text = NewText(lang)
			m.Update(tea.WindowSizeMsg{Width: minWidth, Height: 40})
			tickN(m, 3)
			m.snapshot.Sent, m.snapshot.NotSent = 146, 4

			if line := statLineWith(t, m.body(minWidth), m.text.NotSent()); !strings.Contains(line, "4") {
				t.Errorf("live: %q", line)
			}

			m.done, m.report = true, engine.Report{Sent: 146, Failed: 146, NotSent: 4}
			if line := statLineWith(t, m.body(minWidth), m.text.NotSent()); !strings.Contains(line, "4") {
				t.Errorf("final: %q", line)
			}
		})
	}
}

// Ground: contract — "not sent" appears only when some call did not go out: a
// line saying "not sent 0" on every run is noise the reader learns to skip.
func TestScreensSayNothingWhenEveryCallWentOut(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tickN(m, 3)
	m.snapshot.Sent, m.snapshot.NotSent = 146, 0
	if body := m.body(120); strings.Contains(body, m.text.NotSent()) {
		t.Errorf("live view names unsent calls when there are none:\n%s", body)
	}

	m.done, m.report = true, engine.Report{Sent: 146, Failed: 146}
	if body := m.body(120); strings.Contains(body, m.text.NotSent()) {
		t.Errorf("final screen names unsent calls when there are none:\n%s", body)
	}
}

type unsentSender struct{}

func (unsentSender) Send(_ context.Context, _ engine.Request) (engine.Outcome, error) {
	return engine.Outcome{Category: engine.CategoryTimeout, NotSent: true, Err: context.DeadlineExceeded,
		DoneAt: time.Now()}, nil
}

// Ground: contract — the progress line without a terminal carries the same
// totals as the live view.
func TestPlainProgressCountsTheCallsThatDidNotGoOut(t *testing.T) {
	eng, err := engine.New(engine.Options{
		Calls: []engine.Call{{Method: "a.B/One", Timeout: time.Second,
			Stages: []engine.Stage{{StartRPS: 20, TargetRPS: 20, Duration: 1500 * time.Millisecond}}}},
		Sender: unsentSender{}, MaxInFlight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := RunPlain(&out, "localhost:50051", eng, func() error { return eng.Run(t.Context()) }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not-sent 0 ") || !strings.Contains(out.String(), "not-sent ") {
		t.Errorf("the progress line does not count the unsent calls:\n%s", out.String())
	}
}
