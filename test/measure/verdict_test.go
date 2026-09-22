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
	"sync/atomic"
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
	if !report.Incomplete {
		t.Error("a run stopped by the cap must be incomplete")
	}
	if report.Planned != 2*time.Second || report.Duration >= report.Planned {
		t.Errorf("planned %v, ran %v; want 2s and less", report.Planned, report.Duration)
	}
	// Arithmetic of this run: the cap is ⌈1000×200ms⌉ + 1 + ⌈1000×100ms⌉ = 301
	// slots, and the wrapper frees a slot only 120ms past each deadline, so
	// nothing is released before 320ms. The cap therefore fills at 301ms, and
	// the calls scheduled in the first 101ms are past their 200ms deadline by
	// then. Anything far from 101 means the slots were not held the way the
	// verdict says they were.
	// 0..101ms inclusive is 102 calls; the band allows only the jitter of a
	// real run, and an off-by-one in the counting is pinned exactly by
	// TestPoolCountsHeldSlotsExactlyAndDecidesItsEdges.
	if got := report.CapHit.OverDeadline; got < 95 || got > 110 {
		t.Errorf("over deadline = %d, want about 102: the slots held past their deadline at the hit\n"+
			"start lag max %v, run %v of the planned %v, cap hit at %v, aborted %d, timed out %d",
			got, report.StartLagMax, report.Duration, report.Planned, report.CapHit.At,
			report.Aborted, report.Methods[0].TimedOut)
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

// holdingMethod holds the slots of one method past its deadline and leaves the
// other alone: one method's sender misbehaving, not the whole run's.
type holdingMethod struct {
	engine.Sender
	method string
	hold   time.Duration
}

func (h holdingMethod) Send(ctx context.Context, req engine.Request) (engine.Outcome, error) {
	out, err := h.Sender.Send(ctx, req)

	if req.Method == h.method {
		select {
		case <-time.After(time.Until(req.Deadline.Add(h.hold))):
		case <-ctx.Done():
		}
	}

	return out, err
}

// Two methods share one budget: A with a 200ms timeout holds its slots, B with
// a 2s timeout answers at once and leaves most of its own budget unused. A
// fills that room, so the cap comes about 2.4s in — long after A's deadlines,
// which are 200ms. By then the held slots are seconds past their deadline, not
// milliseconds, and the report must still describe them without claiming how
// far past they were.
func TestReport_TwoMethodsOfDifferentBudgetsStillNameTheHeldSlots(t *testing.T) {
	target := stand.Start(stand.Constant(time.Millisecond))
	t.Cleanup(target.Stop)

	const (
		rate      = 100
		fast      = 2 * time.Second
		slow      = 200 * time.Millisecond
		otherPath = "/grpc.health.v1.Health/Check"
	)

	sender := grpcsender.New(grpcsender.Options{
		Target:      target.Target(),
		DialOptions: []grpc.DialOption{target.DialOption()},
	})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	holding := holdingMethod{Sender: sender, method: otherPath, hold: time.Minute}
	eng, err := engine.New(engine.Options{
		Calls: []engine.Call{
			// A is the stand's method; B is a path the stand does not serve, so
			// it comes back at once and holds a slot for a moment only — which
			// is exactly the shape of a method whose budget stays unused.
			load(otherPath, rate, 5*time.Second, slow),
			load(target.Method()+"/", rate, 5*time.Second, fast),
		},
		Sender:      holding,
		MaxInFlight: budget(rate, slow) + budget(rate, fast),
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()

	if err := eng.Run(ctx); !errors.Is(err, engine.ErrInFlightCapExceeded) {
		t.Fatalf("run = %v, want the cap", err)
	}

	report := eng.Report()
	if report.CapHit == nil {
		t.Fatal("no cap hit in the report")
	}
	// The cap comes around 2.4s; everything of A older than its 200ms deadline
	// is still held, which is the bulk of what is in flight.
	if got := report.CapHit.OverDeadline; got < 150 {
		t.Errorf("over deadline = %d at %v, want the held slots of the method with the short timeout",
			got, report.CapHit.At)
	}
}

// --- the sending rate --------------------------------------------------------

const (
	rateRPS     = 50
	rateRun     = 3 * time.Second
	rateTimeout = time.Second
)

// The drain after the plan is waiting, not sending: divided into the rate, a
// hanging target would read as 150 calls over 4s, 37.5/s instead of 50.
func TestReport_AHangingTargetDoesNotLowerTheSendingRate(t *testing.T) {
	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), rateRPS, rateRun, rateTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := report.Methods[0].RPS; got < rateRPS*0.96 || got > rateRPS*1.04 {
		t.Errorf("rps = %.1f, want %d: the drain is not sending time", got, rateRPS)
	}
}

// stopAt calls Stop once the n-th call reaches the sender.
type stopAt struct {
	engine.Sender
	n    int64
	seen atomic.Int64
	stop func()
}

func (s *stopAt) Send(ctx context.Context, req engine.Request) (engine.Outcome, error) {
	if s.seen.Add(1) == s.n {
		s.stop()
	}

	return s.Sender.Send(ctx, req)
}

// Stopped after a second of a 3s plan: 50 calls over the second they were
// sent in. Dividing by the plan (3s) or by the run with its drain (2s) gives
// 17 or 25.
func TestReport_AStoppedRunRatesOverTheTimeItSent(t *testing.T) {
	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	sender := grpcsender.New(grpcsender.Options{
		Target:      target.Target(),
		DialOptions: []grpc.DialOption{target.DialOption()},
	})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect to the stand: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	wrapped := &stopAt{Sender: sender, n: rateRPS}
	eng, err := engine.New(engine.Options{
		Calls:       []engine.Call{load(target.Method(), rateRPS, rateRun, rateTimeout)},
		Sender:      wrapped,
		MaxInFlight: 1000,
	})
	if err != nil {
		t.Fatalf("build the engine: %v", err)
	}
	wrapped.stop = eng.Stop

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()
	if err := eng.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	report := eng.Report()
	if !report.Incomplete {
		t.Fatal("report not marked incomplete after Stop")
	}
	if got := report.Methods[0].RPS; got < rateRPS*0.9 || got > rateRPS*1.1 {
		t.Errorf("rps = %.1f, want about %d: %d calls over the second they were sent in",
			got, rateRPS, report.Methods[0].Sent)
	}
}

// A target that answers "no such method" refuses every call in microseconds.
// Counted as refusals they would read as a target shedding load; nothing about
// the load was tested at all.
func TestReport_ATargetThatRejectsEveryRequestInvalidatesTheRun(t *testing.T) {
	target := stand.Start(stand.FailEvery(1, codes.Unimplemented, 0))
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, time.Second, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.Rejected.Count != m.Sent || m.Sent == 0 {
		t.Errorf("rejected %d of %d, want all", m.Rejected.Count, m.Sent)
	}
	if m.Refusal.Count != 0 {
		t.Errorf("refused %d, want none: a bad request is not the target shedding load", m.Refusal.Count)
	}
	if !report.RequestRejected {
		t.Error("run not marked as one whose requests the target rejected")
	}
}

// One method rejected, another served: the run still measured something.
func TestReport_ARejectedMethodAloneDoesNotInvalidateTheRun(t *testing.T) {
	target := stand.Start(stand.FailEvery(3, codes.ResourceExhausted, 0))
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, time.Second, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if report.RequestRejected {
		t.Error("an overloaded target was read as a bad request")
	}
	if got := report.Methods[0].Refusal.Count; got == 0 {
		t.Error("refusals not counted")
	}
}

// A run of three methods where one is misspelled: two thirds of the calls are
// served, so a share taken over the run says 33% and no verdict. The verdict
// belongs to the method.
func TestReport_OneRejectedMethodAmongServedOnesIsStillAVerdict(t *testing.T) {
	served := stand.Start(stand.Constant(time.Millisecond))
	t.Cleanup(served.Stop)

	sender := grpcsender.New(grpcsender.Options{
		Target:      served.Target(),
		DialOptions: []grpc.DialOption{served.DialOption()},
	})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	eng, err := engine.New(engine.Options{
		Calls: []engine.Call{
			load(served.Method(), silentRPS, time.Second, silentTimeout),
			load("/grpc.health.v1.Health/Watch", silentRPS, time.Second, silentTimeout),
			load("/grpc.health.v1.Health/Typo", silentRPS, time.Second, silentTimeout),
		},
		Sender:      sender,
		MaxInFlight: 1000,
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()
	if err := eng.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	report := eng.Report()
	if !report.RequestRejected {
		t.Error("one method rejected every call, yet the run passed as valid")
	}

	var rejected []string
	for _, m := range report.Methods {
		if m.Rejected.Count > 0 && m.Rejected.Count == m.Sent {
			rejected = append(rejected, m.Method)
		}
	}
	if len(rejected) != 2 {
		t.Errorf("methods rejected outright = %v, want the typo and the streaming one", rejected)
	}
}

// The moment the answers stopped, measured end to end: the stand goes silent
// 1s after its first call, and the statement must name that moment rather than
// the second it falls into.
func TestReport_TheLastAnswerIsWhereTheSilenceBegins(t *testing.T) {
	// Frozen for a minute from the first second: every later call waits out
	// its own timeout, so the target is silent from then on.
	target := stand.Start(stand.Frozen(time.Second, time.Minute, time.Millisecond))
	t.Cleanup(target.Stop)

	report, err := runOn(t, target, load(target.Method(), silentRPS, silentRun, silentTimeout), 1000, asIs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	m := report.Methods[0]
	if m.LastAnswerAt == nil {
		t.Fatal("no last answer, yet the target answered the first second")
	}
	// The stand counts from its first arrival, which comes after the run
	// starts, so the moment lands just past a second, never before it.
	if *m.LastAnswerAt < time.Second || *m.LastAnswerAt > 1200*time.Millisecond {
		t.Errorf("last answer at %v, want about 1s", *m.LastAnswerAt)
	}
}
