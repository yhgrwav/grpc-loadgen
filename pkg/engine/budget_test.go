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
	if budget.Need != 20_000 || budget.Cap != 5000 || budget.TotalRPS != 1000 {
		t.Errorf("budget = %+v, want Need 20000, Cap 5000, TotalRPS 1000", *budget)
	}
}

func TestNew_BudgetExactlyAtTheCapIsAccepted(t *testing.T) {
	if err := newWithCap(5000, budgetCall("a", 100, 50*time.Second)); err != nil {
		t.Errorf("100 RPS x 50s = 5000 against a cap of 5000: err = %v, want nil", err)
	}
}

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
	if budget.Need != 6000 || budget.TotalRPS != 3000 {
		t.Errorf("budget = %+v, want Need 6000, TotalRPS 3000", *budget)
	}
}

func TestNew_CallWithoutTimeoutIsLeftOutOfTheBudget(t *testing.T) {
	// No deadline is an explicit choice for a library caller; there is no
	// bound to check it against.
	err := newWithCap(10,
		budgetCall("unbounded", 100_000, 0),
		budgetCall("bounded", 5, time.Second),
	)
	if err != nil {
		t.Errorf("err = %v, want nil: only the bounded call counts, and 5 <= 10", err)
	}
}

func TestNew_HugeBudgetDoesNotOverflowIntoAPass(t *testing.T) {
	// MaxInt32 RPS for 292 years is about 2e19 requests, past what an int holds.
	// A product that wraps around lands anywhere, a negative number included,
	// and would pass the check.
	err := newWithCap(math.MaxInt/2, budgetCall("a", math.MaxInt32, time.Duration(math.MaxInt64)))
	if !errors.Is(err, ErrInFlightBudget) {
		t.Errorf("err = %v, want ErrInFlightBudget", err)
	}
}
