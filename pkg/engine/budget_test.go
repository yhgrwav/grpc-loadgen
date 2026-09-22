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
	"errors"
	"math"
	"testing"
	"time"
)

// budgetCall is a constant-rate call with a deadline.
func budgetCall(method string, rps int, timeout time.Duration) Call {
	return Call{
		Method:  method,
		Timeout: timeout,
		Stages:  []Stage{{StartRPS: rps, TargetRPS: rps, Duration: time.Second}},
	}
}

func newWithCap(maxInFlight int, calls ...Call) error {
	_, err := New(Options{Calls: calls, Sender: FakeSender{}, MaxInFlight: maxInFlight})

	return err
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_RejectsCallsThatOutgrowTheCapWhenTheTargetHangs(t *testing.T) {
	// 1000 RPS against a hung target with a 20s timeout: the cap of 5000 is
	// hit at 5s, fifteen seconds before the first timeout could free a slot.
	err := newWithCap(5000, budgetCall("a", 1000, 20*time.Second))

	if !errors.Is(err, ErrInFlightBudget) {
		t.Fatalf("err = %v, want ErrInFlightBudget", err)
	}

	var budget *InFlightBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("err = %T, want *InFlightBudgetError carrying the numbers", err)
	}
	if budget.Need != 20_000 || budget.Cap != 5000 || budget.PeakRPS != 1000 {
		t.Errorf("budget = %+v, want Need 20000, Cap 5000, PeakRPS 1000", *budget)
	}
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_BudgetExactlyAtTheCapIsAccepted(t *testing.T) {
	if err := newWithCap(5000, budgetCall("a", 100, 50*time.Second)); err != nil {
		t.Errorf("100 RPS x 50s = 5000 against a cap of 5000: err = %v, want nil", err)
	}
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_BudgetOneOverTheCapIsRejected(t *testing.T) {
	// 100 x 50.001s = 5000.1, which rounds up: a request is either in flight
	// or not.
	err := newWithCap(5000, budgetCall("a", 100, 50*time.Second+time.Millisecond))

	var budget *InFlightBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("err = %v, want *InFlightBudgetError", err)
	}
	if budget.Need != 5001 {
		t.Errorf("Need = %d, want 5001", budget.Need)
	}
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_BudgetSumsEveryCall(t *testing.T) {
	// The cap is shared by the whole run: two calls that fit one by one can
	// still overflow it together.
	err := newWithCap(5000,
		budgetCall("a", 1500, 2*time.Second),
		budgetCall("b", 1500, 2*time.Second),
	)

	var budget *InFlightBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("err = %v, want *InFlightBudgetError", err)
	}
	if budget.Need != 6000 || budget.PeakRPS != 3000 {
		t.Errorf("budget = %+v, want Need 6000, PeakRPS 3000", *budget)
	}
}

// Ground: contract — engine.New rejects what it cannot budget, whoever calls it.
func TestNew_CallWithoutTimeoutIsRejected(t *testing.T) {
	// A zero Timeout means no deadline, and no deadline is an infinite wait: a
	// hung target holds the call's requests until the cap runs out. Infinity
	// times any rate exceeds any cap.
	err := newWithCap(1_000_000,
		budgetCall("unbounded", 1, 0),
		budgetCall("bounded", 5, time.Second),
	)

	var budget *InFlightBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("err = %v, want *InFlightBudgetError", err)
	}
	if len(budget.Unbounded) != 1 || budget.Unbounded[0] != "unbounded" {
		t.Errorf("Unbounded = %v, want the call without a timeout named", budget.Unbounded)
	}
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_StagesShorterThanTheTimeoutAddUp(t *testing.T) {
	// Two one-second stages at 1000 RPS under a 2s timeout: at t=2s a hung
	// target still holds every request of both, 2000 in all. A check that sized
	// each stage by its own duration would count 1000 and let a cap of 1999 pass.
	call := Call{
		Method:  "a",
		Timeout: 2 * time.Second,
		Stages: []Stage{
			{StartRPS: 1000, TargetRPS: 1000, Duration: time.Second},
			{StartRPS: 1000, TargetRPS: 1000, Duration: time.Second},
		},
	}

	if err := newWithCap(1999, call); !errors.Is(err, ErrInFlightBudget) {
		t.Errorf("err = %v, want ErrInFlightBudget: 2000 requests fit no cap of 1999", err)
	}
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_BudgetUsesThePeakStage(t *testing.T) {
	// Neither the first stage nor the sum of stages: the busiest one.
	call := Call{
		Method:  "a",
		Timeout: time.Second,
		Stages: []Stage{
			{StartRPS: 100, TargetRPS: 100, Duration: time.Minute},
			{StartRPS: 900, TargetRPS: 900, Duration: time.Minute},
			{StartRPS: 100, TargetRPS: 100, Duration: time.Minute},
		},
	}

	var budget *InFlightBudgetError
	if err := newWithCap(1, call); !errors.As(err, &budget) {
		t.Fatalf("err = %v, want *InFlightBudgetError", err)
	}
	if budget.PeakRPS != 900 || budget.Need != 900 {
		t.Errorf("budget = %+v, want PeakRPS 900 and Need 900", *budget)
	}
}

// Ground: boundary — exactly at the cap, one over it, overflow: a run cannot hit these edges
// reliably.
func TestNew_HugeBudgetDoesNotOverflowIntoAPass(t *testing.T) {
	// MaxInt32 RPS for 292 years is about 2e19 requests, past what an int holds.
	// A product that wraps around lands anywhere, a negative number included,
	// and would pass the check.
	err := newWithCap(math.MaxInt/2, budgetCall("a", math.MaxInt32, time.Duration(math.MaxInt64)))
	if !errors.Is(err, ErrInFlightBudget) {
		t.Errorf("err = %v, want ErrInFlightBudget", err)
	}
}
