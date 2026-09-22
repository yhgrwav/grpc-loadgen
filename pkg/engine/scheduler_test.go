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
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func collect(ctx context.Context, t *testing.T, s *Scheduler) ([]Request, error) {
	t.Helper()

	out := make(chan Request, 4096)
	errc := make(chan error, 1)

	go func() {
		errc <- s.Run(ctx, out)
		close(out)
	}()

	var got []Request
	for req := range out {
		got = append(got, req)
	}

	return got, <-errc
}

// Ground: boundary — the exact count at a stage end: checkArrivals tolerates one call, so losing
// the last one stays green end-to-end (mutation 2026-09-22).
func TestRunEmitsOneRequestPerInterval(t *testing.T) {
	s := NewScheduler(Call{Method: "a.B/C", Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 100 * time.Millisecond}}})

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(got) != 10 {
		t.Fatalf("got %d requests, want 10", len(got))
	}

	for i := 1; i < len(got); i++ {
		gap := got[i].ScheduledAt.Sub(got[i-1].ScheduledAt)
		if gap != 10*time.Millisecond {
			t.Errorf("gap %d = %s, want 10ms", i, gap)
		}
	}
}

// Ground: contract — Scheduler is exported; its requests are what a library caller sends.
func TestRunWalksEveryStage(t *testing.T) {
	s := NewScheduler(Call{Method: "a.B/C", Stages: []Stage{
		{StartRPS: 100, TargetRPS: 100, Duration: 50 * time.Millisecond},
		{StartRPS: 200, TargetRPS: 200, Duration: 50 * time.Millisecond},
	}})

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(got) != 15 {
		t.Fatalf("got %d requests, want 15", len(got))
	}
}

// Ground: boundary — drift of microseconds per stage, below what a run against the stand resolves.
func TestStagesDoNotDriftApart(t *testing.T) {
	s := NewScheduler(Call{Method: "a.B/C", Stages: []Stage{
		{StartRPS: 100, TargetRPS: 100, Duration: 50 * time.Millisecond},
		{StartRPS: 100, TargetRPS: 100, Duration: 50 * time.Millisecond},
	}})

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	span := got[len(got)-1].ScheduledAt.Sub(got[0].ScheduledAt)
	if want := 90 * time.Millisecond; span != want {
		t.Errorf("span = %s, want %s", span, want)
	}
}

// Ground: boundary — rounding of nanoseconds per interval, below what a run against the stand
// resolves.
func TestScheduledTimeDoesNotAccumulateRounding(t *testing.T) {
	const rps = 3000

	s := NewScheduler(Call{Method: "a.B/C", Stages: []Stage{{StartRPS: rps, TargetRPS: rps, Duration: 100 * time.Millisecond}}})

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	last := got[len(got)-1]
	want := got[0].ScheduledAt.Add(time.Duration(len(got)-1) * time.Second / rps)

	if !last.ScheduledAt.Equal(want) {
		t.Errorf("last scheduled at %s, want %s", last.ScheduledAt, want)
	}
}

// Ground: contract — Scheduler is exported; its requests are what a library caller sends.
func TestRunStopsOnCancel(t *testing.T) {
	s := NewScheduler(Call{Method: "a.B/C", Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: time.Hour}}})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	got, err := collect(ctx, t, s)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want %v", err, context.DeadlineExceeded)
	}
	if len(got) == 0 {
		t.Error("no requests emitted before cancel")
	}
	if len(got) > 20 {
		t.Errorf("emitted %d requests, want the run to stop early", len(got))
	}
}

// Ground: contract — Scheduler is exported; its requests are what a library caller sends.
func TestRequestsCarryCallFields(t *testing.T) {
	call := Call{
		Method:       "a.B/C",
		Payload:      []byte("payload"),
		KeepResponse: true,
		Stages:       []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 20 * time.Millisecond}},
	}
	s := NewScheduler(call)

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no requests emitted")
	}

	for i, req := range got {
		if req.Method != call.Method {
			t.Errorf("request %d method = %q, want %q", i, req.Method, call.Method)
		}
		if !bytes.Equal(req.Payload, call.Payload) {
			t.Errorf("request %d payload = %q, want %q", i, req.Payload, call.Payload)
		}
		if req.KeepResponse != call.KeepResponse {
			t.Errorf("request %d KeepResponse = %v, want %v", i, req.KeepResponse, call.KeepResponse)
		}
	}
}

// Ground: contract — Scheduler is exported; its requests are what a library caller sends.
func TestRequestDeadlineFollowsTimeout(t *testing.T) {
	const timeout = 25 * time.Millisecond

	call := Call{
		Method:  "a.B/C",
		Timeout: timeout,
		Stages:  []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 20 * time.Millisecond}},
	}
	s := NewScheduler(call)

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no requests emitted")
	}

	for i, req := range got {
		want := req.ScheduledAt.Add(timeout)
		if !req.Deadline.Equal(want) {
			t.Errorf("request %d deadline = %s, want %s", i, req.Deadline, want)
		}
	}
}

// Ground: contract — Scheduler is exported; its requests are what a library caller sends.
func TestRequestDeadlineIsZeroWithoutTimeout(t *testing.T) {
	call := Call{
		Method: "a.B/C",
		Stages: []Stage{{StartRPS: 100, TargetRPS: 100, Duration: 20 * time.Millisecond}},
	}
	s := NewScheduler(call)

	got, err := collect(context.Background(), t, s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no requests emitted")
	}

	for i, req := range got {
		if !req.Deadline.IsZero() {
			t.Errorf("request %d deadline = %s, want zero", i, req.Deadline)
		}
	}
}

// Ground: contract — Scheduler is exported; its requests are what a library caller sends.
func TestRunRejectsBadStages(t *testing.T) {
	tests := []struct {
		name    string
		stages  []Stage
		wantErr error
	}{
		{
			name:    "no stages",
			stages:  nil,
			wantErr: ErrNoStages,
		},
		{
			name:    "zero rps",
			stages:  []Stage{{StartRPS: 0, TargetRPS: 0, Duration: time.Second}},
			wantErr: ErrStageRPS,
		},
		{
			name:    "negative rps",
			stages:  []Stage{{StartRPS: -1, TargetRPS: -1, Duration: time.Second}},
			wantErr: ErrStageRPS,
		},
		{
			name:    "ramp is not supported yet",
			stages:  []Stage{{StartRPS: 0, TargetRPS: 500, Duration: time.Second}},
			wantErr: ErrRampNotYet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewScheduler(Call{Method: "a.B/C", Stages: tt.stages})

			_, err := collect(context.Background(), t, s)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
