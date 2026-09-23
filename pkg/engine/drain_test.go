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

package engine

import (
	"context"
	"testing"
	"time"
)

// hungTarget answers nothing: every call returns at its deadline and not a
// moment before, cancelled or not, so a cancelled run drains for a timeout.
type hungTarget struct{}

func (hungTarget) Send(_ context.Context, req Request) (Outcome, error) {
	time.Sleep(time.Until(req.Deadline)) // the hang under test, not synchronisation

	return Outcome{Category: CategoryTimeout, Err: context.DeadlineExceeded, DoneAt: time.Now()}, nil
}

// Ground: contract — sent/s is what the generator drove while it sent. A run
// cancelled at 3s of a 10s plan sent for 3s; the 2s its calls then take to
// time out send nothing. Over the plan the rate would read 300, over the run
// with its drain 600; driven, it was 1000.
func TestEngine_CancelledRunRatesOverTheSendingWindowNotTheDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for five seconds")
	}

	eng, err := New(Options{
		Calls: []Call{{Method: "a", Timeout: 2 * time.Second,
			Stages: []Stage{{StartRPS: 1000, TargetRPS: 1000, Duration: 10 * time.Second}}}},
		Sender:      hungTarget{},
		MaxInFlight: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(3*time.Second, cancel)
	start := time.Now()
	_ = eng.Run(ctx)
	ran := time.Since(start)

	report := eng.Report()
	// The run must have drained, or the test does not tell the windows apart.
	if ran < 4*time.Second {
		t.Fatalf("the run took %v: no drain after the cancel at 3s", ran)
	}
	// Between the wrong answers (300, 600) and the right one, with room for
	// a scheduler a few percent behind.
	if report.Methods[0].RPS < 900 || report.Methods[0].RPS > 1100 {
		t.Errorf("sent/s = %.0f over a run of %v, %d sent; want about 1000: 3s of sending",
			report.Methods[0].RPS, ran, report.Sent)
	}
}
