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

package measure

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/grpcsender"
	"github.com/yhgrwav/leettest/test/stand"
)

// releaseMargin is the budget's allowance for a slot released after its
// deadline (decisions.md, "Запас бюджета…").
const releaseMargin = engine.ReleaseMargin

// holdingPastDeadline returns every call only hold after its deadline: a
// generator late to release its slots, set exactly rather than left to the
// scheduler of the machine the test runs on.
type holdingPastDeadline struct {
	engine.Sender
	hold time.Duration
}

func (h holdingPastDeadline) Send(ctx context.Context, req engine.Request) (engine.Outcome, error) {
	out, err := h.Sender.Send(ctx, req)

	select {
	case <-time.After(time.Until(req.Deadline.Add(h.hold))):
	case <-ctx.Done():
	}

	return out, err
}

// budget is what engine.New asks of a call at a constant rate:
// ⌈rps × timeout⌉ + 1 + ⌈rps × releaseMargin⌉.
func budget(rps int, timeout time.Duration) int {
	ceil := func(d time.Duration) int {
		return int((time.Duration(rps)*d + time.Second - 1) / time.Second)
	}

	return ceil(timeout) + 1 + ceil(releaseMargin)
}

// runOn runs call against the stand through wrap and returns what the run
// ended with, cap or not.
func runOn(t *testing.T, s *stand.Stand, call engine.Call, maxInFlight int,
	wrap func(engine.Sender) engine.Sender,
) (engine.Report, error) {
	t.Helper()

	sender := grpcsender.New(grpcsender.Options{
		Target:      s.Target(),
		DialOptions: []grpc.DialOption{s.DialOption()},
	})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect to the stand: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	eng, err := engine.New(engine.Options{
		Calls:       []engine.Call{call},
		Sender:      wrap(sender),
		MaxInFlight: maxInFlight,
	})
	if err != nil {
		t.Fatalf("build the engine: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()

	err = eng.Run(ctx)

	return eng.Report(), err
}

// --- the in-flight cap ---------------------------------------------------

const (
	capRPS     = 1000
	capTimeout = 200 * time.Millisecond
)

func TestReport_SlotsHeldPastTheAllowanceHitTheCap(t *testing.T) {
	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	// 20ms past the allowance at 1000 RPS is 20 slots over the budget: far
	// above the scheduler's own lateness under -race, a few slots.
	hold := releaseMargin + 20*time.Millisecond
	report, err := runOn(t, target, load(target.Method(), capRPS, 2*time.Second, capTimeout),
		budget(capRPS, capTimeout), func(s engine.Sender) engine.Sender { return holdingPastDeadline{s, hold} })

	if !errors.Is(err, engine.ErrInFlightCapExceeded) {
		t.Fatalf("run = %v, want the cap: slots were held %v past their deadline", err, hold)
	}
	if report.CapHit == nil {
		t.Fatal("report has no CapHit")
	}
	if report.CapHit.Unsent != 1 {
		t.Errorf("unsent = %d, want 1: the cap refuses the call that did not fit, and the run stops", report.CapHit.Unsent)
	}
	if report.CapHit.OverDeadline == 0 {
		t.Error("no call in flight was past its deadline, yet only such calls fill the cap")
	}
	if !report.Incomplete {
		t.Error("a run stopped by the cap must be incomplete")
	}
	if report.Planned != 2*time.Second || report.Duration >= report.Planned {
		t.Errorf("planned %v, ran %v; want 2s and less", report.Planned, report.Duration)
	}
}

func TestReport_SlotsHeldWithinTheAllowanceDoNotHitTheCap(t *testing.T) {
	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	hold := releaseMargin - 20*time.Millisecond
	report, err := runOn(t, target, load(target.Method(), capRPS, 2*time.Second, capTimeout),
		budget(capRPS, capTimeout), func(s engine.Sender) engine.Sender { return holdingPastDeadline{s, hold} })

	if err != nil {
		t.Fatalf("run = %v, want none: %v past the deadline is within the %v allowance", err, hold, releaseMargin)
	}
	if report.CapHit != nil || report.Incomplete {
		t.Errorf("cap hit %+v, incomplete %v; want neither", report.CapHit, report.Incomplete)
	}
}

// --- what the target did -------------------------------------------------

func asIs(s engine.Sender) engine.Sender { return s }

const (
	silentRPS     = 50
	silentRun     = 3 * time.Second
	silentTimeout = 300 * time.Millisecond
)

func TestReport_AHangingTargetIsSilentFromTheFirstSecond(t *testing.T) {
	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, silentRun, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.TimedOut != m.Sent || m.Sent == 0 {
		t.Errorf("timed out %d of %d, want all", m.TimedOut, m.Sent)
	}
	if m.SilentFrom == nil || *m.SilentFrom != 0 {
		t.Errorf("silent from %v, want second 0", deref(m.SilentFrom))
	}
	if m.RPSLow != silentRPS || m.RPSHigh != silentRPS {
		t.Errorf("rates %d-%d, want %d", m.RPSLow, m.RPSHigh, silentRPS)
	}
}

func TestReport_ATargetThatStopsIsSilentFromThatSecond(t *testing.T) {
	// Frozen from 0.9s past the end of the run: every call from then on waits
	// out its timeout. The stand counts from the first arrival, the timeline
	// from the engine's start, and under load the first call arrives
	// milliseconds late: frozen at exactly 1s, the start of second 1 would
	// still be answered. Second 0 keeps answers either way.
	target := stand.Start(stand.Frozen(900*time.Millisecond, time.Minute, time.Millisecond))
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, silentRun, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.SilentFrom == nil || *m.SilentFrom != 1 {
		t.Errorf("silent from %v, want second 1", deref(m.SilentFrom))
	}
	// The last 2.1s: 105 calls, give or take the arrival delay.
	if m.TimedOut < 2*silentRPS || m.TimedOut > 2*silentRPS+silentRPS/10+2 {
		t.Errorf("timed out %d, want the calls of the last 2.1s", m.TimedOut)
	}
}

func TestReport_ATargetThatAnswersSomeIsNeverSilent(t *testing.T) {
	// Every third call hangs, the rest are answered at once, to the end.
	target := stand.Start(func(c stand.Call) stand.Behavior {
		return stand.Behavior{Hang: c.N%3 == 0, Delay: time.Millisecond}
	})
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, silentRun, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.SilentFrom != nil {
		t.Errorf("silent from %d, want none: two calls in three were answered to the end", *m.SilentFrom)
	}
	if want := m.Sent / 3; m.TimedOut < want-1 || m.TimedOut > want+1 {
		t.Errorf("timed out %d of %d, want a third", m.TimedOut, m.Sent)
	}
}

func TestReport_ATargetThatOnlyRefusesAnswers(t *testing.T) {
	target := stand.Start(stand.FailEvery(1, codes.Unavailable, time.Millisecond))
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, silentRun, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.TimedOut != 0 || m.SilentFrom != nil {
		t.Errorf("timed out %d, silent from %v; want 0 and none: a refusal is an answer", m.TimedOut, m.SilentFrom)
	}
	if m.Failed != m.Sent {
		t.Errorf("failed %d of %d, want all refused", m.Failed, m.Sent)
	}
}

// deref prints a second that may be absent.
func deref(p *int) any {
	if p == nil {
		return "none"
	}

	return *p
}

func TestReport_ARefusalIsAnAnswerAmongTimeouts(t *testing.T) {
	// Every other call hangs, the rest are refused at once, to the end: the
	// target answers, if only with no.
	target := stand.Start(func(c stand.Call) stand.Behavior {
		if c.N%2 == 0 {
			return stand.Behavior{Hang: true}
		}

		return stand.Behavior{Code: codes.Unavailable, Delay: time.Millisecond}
	})
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, silentRun, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.SilentFrom != nil {
		t.Errorf("silent from %d, want none: a refusal is an answer", *m.SilentFrom)
	}
	if want := m.Sent / 2; m.TimedOut < want-1 || m.TimedOut > want+1 {
		t.Errorf("timed out %d of %d, want half", m.TimedOut, m.Sent)
	}
}

func TestReport_AHangingTargetFailsEveryCallLiveAndInTheReport(t *testing.T) {
	// Timeouts left TargetFailed for an outcome of their own; the error share
	// the live view and the report print must still count them.
	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	sender := grpcsender.New(grpcsender.Options{Target: target.Target(), DialOptions: []grpc.DialOption{target.DialOption()}})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect to the stand: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	eng, err := engine.New(engine.Options{
		Calls:       []engine.Call{load(target.Method(), silentRPS, silentRun, silentTimeout)},
		Sender:      sender,
		MaxInFlight: 1000,
	})
	if err != nil {
		t.Fatalf("build the engine: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()
	if err := eng.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	live := eng.Snapshot()
	if live.Sent == 0 || live.Failed != live.Sent {
		t.Errorf("live view: failed %d of %d, want all: no call got an answer", live.Failed, live.Sent)
	}
	for _, m := range live.Methods {
		if m.Failed != m.Sent {
			t.Errorf("live view, %s: failed %d of %d, want all", m.Method, m.Failed, m.Sent)
		}
	}

	report := eng.Report()
	if report.Failed != report.Sent || report.Methods[0].Failed != report.Methods[0].Sent {
		t.Errorf("report: failed %d of %d, method %d of %d; want all",
			report.Failed, report.Sent, report.Methods[0].Failed, report.Methods[0].Sent)
	}
}
