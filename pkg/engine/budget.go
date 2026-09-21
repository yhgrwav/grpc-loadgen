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
	"fmt"
	"math"
	"math/bits"
	"time"
)

var ErrInFlightBudget = errors.New("calls can outgrow the in-flight cap before any request times out")

// InFlightBudgetError says how many requests a hung target would hold in
// flight. By Little's law that is rate × wait, and against a target that has
// stopped answering the wait is the timeout: when the sum exceeds the cap, the
// cap is hit before the first timeout, and the run ends with a cap error
// instead of a single censored observation.
type InFlightBudgetError struct {
	// Need is Σ ⌈RPS × timeout⌉ over the calls that have a timeout.
	Need int
	Cap  int
	// TotalRPS is the summed peak rate of those calls, so the caller can say
	// which timeout would fit: Cap / TotalRPS.
	TotalRPS int
}

func (e *InFlightBudgetError) Error() string {
	return fmt.Sprintf("%v: a target that stops answering would hold %d requests in flight "+
		"(rps × timeout), and the cap is %d", ErrInFlightBudget, e.Need, e.Cap)
}

func (e *InFlightBudgetError) Unwrap() error { return ErrInFlightBudget }

// checkInFlightBudget rejects calls that a hung target would push past the cap.
// A call without a timeout has no bound to check and is left out.
func checkInFlightBudget(calls []Call, maxInFlight int) error {
	need, totalRPS := 0, 0

	for _, call := range calls {
		if call.Timeout <= 0 {
			continue
		}

		rps := peakRPS(call)
		need = saturatingAdd(need, inFlightFor(rps, call.Timeout))
		totalRPS = saturatingAdd(totalRPS, rps)
	}

	if need > maxInFlight {
		return &InFlightBudgetError{Need: need, Cap: maxInFlight, TotalRPS: totalRPS}
	}

	return nil
}

func peakRPS(call Call) int {
	peak := 0
	for _, stage := range call.Stages {
		peak = max(peak, stage.StartRPS, stage.TargetRPS)
	}

	return peak
}

// inFlightFor is ⌈rps × timeout⌉, saturating at MaxInt: a product that wrapped
// around would pass the check.
func inFlightFor(rps int, timeout time.Duration) int {
	if rps <= 0 {
		return 0
	}

	hi, lo := bits.Mul64(uint64(rps), uint64(timeout))
	if hi >= uint64(time.Second) {
		return math.MaxInt
	}

	quo, rem := bits.Div64(hi, lo, uint64(time.Second))
	if rem > 0 {
		quo++
	}
	if quo > math.MaxInt {
		return math.MaxInt
	}

	return int(quo)
}

func saturatingAdd(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}

	return a + b
}
