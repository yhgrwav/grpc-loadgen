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
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestEngineRunsEveryCall(t *testing.T) {
	eng, err := New(Options{
		Calls: []Call{
			{Method: "a.B/One", Timeout: 100 * time.Millisecond, Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 100 * time.Millisecond}}},
			{Method: "a.B/Two", Timeout: 100 * time.Millisecond, Stages: []Stage{{StartRPS: 50, TargetRPS: 50, Duration: 100 * time.Millisecond}}},
		},
		Sender:      FakeSender{Delay: time.Millisecond},
		MaxInFlight: 64,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	report := eng.Report()

	if report.Sent != 15 {
		t.Errorf("sent = %d, want 15", report.Sent)
	}
	if len(report.Methods) != 2 {
		t.Fatalf("methods = %d, want 2", len(report.Methods))
	}
	if report.Methods[0].Sent != 10 || report.Methods[1].Sent != 5 {
		t.Errorf("per-method counts = %d and %d, want 10 and 5",
			report.Methods[0].Sent, report.Methods[1].Sent)
	}
}

func TestEngineKeepsWarmupOutOfLatencies(t *testing.T) {
	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: 100 * time.Millisecond, Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 100 * time.Millisecond}}}},
		Sender:      FakeSender{Delay: time.Millisecond},
		MaxInFlight: 64,
		Warmup:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	report := eng.Report()

	// Warmup is left out of the counters as well as of the latencies: the report
	// describes the measured half of the run, and counting the other half would
	// halve the reported rate.
	if report.Sent != 5 {
		t.Errorf("sent = %d, want 5 of the 10 requests", report.Sent)
	}
	if got := report.Methods[0].Latencies; got != 5 {
		t.Errorf("latencies kept = %d, want 5 of the 10 requests", got)
	}
}

func TestEngineStopsOnCancel(t *testing.T) {
	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: 100 * time.Millisecond, Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: time.Hour}}}},
		Sender:      FakeSender{Delay: time.Millisecond},
		MaxInFlight: 64,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	err = eng.Run(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want %v", err, context.DeadlineExceeded)
	}
	if eng.Report().Sent == 0 {
		t.Error("no requests recorded before cancel")
	}
}

func TestEngineRunDoesNotLeakSchedulerGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	sender := senderFunc(func(context.Context, Request) (Outcome, error) {
		return Outcome{}, errors.New("sender unusable")
	})

	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: 4 * time.Millisecond, Stages: []Stage{{StartRPS: 1000, TargetRPS: 1000, Duration: time.Hour}}}},
		Sender:      sender,
		MaxInFlight: 4,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := eng.Run(ctx); err == nil {
		t.Fatal("run: want an error from the unusable sender")
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want back to %d", runtime.NumGoroutine(), before)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEngineRejectsBadOptions(t *testing.T) {
	stage := []Stage{{StartRPS: 1, TargetRPS: 1, Duration: time.Second}}

	tests := []struct {
		name    string
		opts    Options
		wantErr error
	}{
		{
			name:    "no calls",
			opts:    Options{Sender: FakeSender{}, MaxInFlight: 1},
			wantErr: ErrNoCalls,
		},
		{
			name:    "no sender",
			opts:    Options{Calls: []Call{{Method: "a.B/C", Stages: stage}}, MaxInFlight: 1},
			wantErr: ErrNoSender,
		},
		{
			name:    "no in-flight room",
			opts:    Options{Calls: []Call{{Method: "a.B/C", Stages: stage}}, Sender: FakeSender{}},
			wantErr: ErrInvalidInFlightCap,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.opts)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSnapshotTracksProgress(t *testing.T) {
	eng, err := New(Options{
		Calls:       []Call{{Method: "a.B/One", Timeout: 100 * time.Millisecond, Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 200 * time.Millisecond}}}},
		Sender:      FakeSender{Delay: 5 * time.Millisecond},
		MaxInFlight: 64,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- eng.Run(context.Background()) }()

	time.Sleep(100 * time.Millisecond)
	mid := eng.Snapshot()

	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	if mid.Sent == 0 {
		t.Error("snapshot taken mid-run reports nothing sent")
	}
	if mid.Total != 200*time.Millisecond {
		t.Errorf("planned duration = %s, want 200ms", mid.Total)
	}
	if eng.Snapshot().Sent < mid.Sent {
		t.Error("final snapshot reports fewer requests than the mid-run one")
	}
}

// A call begun in the last planned second against a target that hangs runs on
// to its deadline past the plan: the reserve must hold it, and in flight must
// come back to zero.
func TestEngineTimelineHoldsTheDrain(t *testing.T) {
	eng, err := New(Options{
		Calls: []Call{{
			Method:  "a.B/C",
			Timeout: 1500 * time.Millisecond,
			Stages:  []Stage{{StartRPS: 20, TargetRPS: 20, Duration: time.Second}},
		}},
		Sender:      FakeSender{Delay: time.Minute},
		MaxInFlight: 64,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	m := eng.Report().Methods[0]
	if m.OutsideTimeline != 0 {
		t.Errorf("outside = %d, want 0", m.OutsideTimeline)
	}

	var begun, failed int
	for _, s := range m.Seconds {
		begun += s.Begun
		failed += s.TargetFailed
	}
	if begun != 20 || failed != 20 {
		t.Errorf("begun %d, failed %d; want all 20 timed out on the timeline", begun, failed)
	}
	if len(m.Seconds) < 3 || m.Seconds[len(m.Seconds)-1].InFlight != 0 {
		t.Errorf("seconds = %+v, want the drain past second 2 and zero in flight at the end", m.Seconds)
	}
}
