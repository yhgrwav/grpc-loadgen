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
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type stopCalls struct {
	stop, abort, exit atomic.Int32
	exited            chan struct{}
}

func newStopCalls() *stopCalls { return &stopCalls{exited: make(chan struct{}, 4)} }

func (c *stopCalls) stopper(grace time.Duration) *Stopper {
	return NewStopper(
		func() { c.stop.Add(1) },
		func() { c.abort.Add(1) },
		func() { c.exit.Add(1); c.exited <- struct{}{} },
		grace,
	)
}

func TestStopper_FirstPressStopsGently(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	if stage := s.Press(); stage != StageStop {
		t.Fatalf("stage = %v, want StageStop", stage)
	}
	if c.stop.Load() != 1 || c.abort.Load() != 0 || c.exit.Load() != 0 {
		t.Errorf("stop/abort/exit = %d/%d/%d, want 1/0/0", c.stop.Load(), c.abort.Load(), c.exit.Load())
	}
}

func TestStopper_SecondPressAborts(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Press()
	if stage := s.Press(); stage != StageAbort {
		t.Fatalf("stage = %v, want StageAbort", stage)
	}
	if c.abort.Load() != 1 || c.exit.Load() != 0 {
		t.Errorf("abort/exit = %d/%d, want 1/0", c.abort.Load(), c.exit.Load())
	}
}

func TestStopper_ThirdPressExitsAtOnce(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Press()
	s.Press()
	if stage := s.Press(); stage != StageExit {
		t.Fatalf("stage = %v, want StageExit", stage)
	}
	if c.exit.Load() != 1 {
		t.Errorf("exit = %d, want 1: the third press is the way out that depends on nothing", c.exit.Load())
	}
}

func TestStopper_AbortThatHangsExitsAfterGrace(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(50 * time.Millisecond)

	s.Press()
	s.Press() // nobody calls Finish: recording or the report hung

	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit after the grace period: a hung abort leaves only kill")
	}
}

func TestStopper_FinishedAbortDoesNotExit(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(50 * time.Millisecond)

	s.Press()
	s.Press()
	s.Finish()

	select {
	case <-c.exited:
		t.Fatal("exit fired after the run and the report finished")
	case <-time.After(200 * time.Millisecond): // 4× the grace: the timer had its chance
	}
}

func TestStopper_PressAfterFinishDoesNothing(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Finish()
	s.Press()

	if c.stop.Load() != 0 {
		t.Error("a press after the end stopped a run that is already over")
	}
}

// --- the live view ------------------------------------------------------

func TestQuitStopsAndLeavesOnceDrained(t *testing.T) {
	c := newStopCalls()
	m := testModel(t)
	m.stopper = c.stopper(time.Hour)

	if _, cmd := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune("q")})); cmd != nil {
		t.Error("the view closed before the calls in flight drained")
	}
	if c.stop.Load() != 1 {
		t.Fatal("the first q did not stop the run")
	}

	// One q still leaves, as before: once the drain ends, the view closes.
	if _, cmd := m.Update(doneMsg{}); cmd == nil {
		t.Error("the view stayed open after the drain: one q must be enough to leave")
	}
}

func TestSecondQuitAbortsAndLeaves(t *testing.T) {
	for _, key := range []tea.Key{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyRunes, Runes: []rune("й")},
	} {
		t.Run(key.String(), func(t *testing.T) {
			c := newStopCalls()
			m := testModel(t)
			m.stopper = c.stopper(time.Hour)

			m.onKey(tea.KeyMsg(key))
			_, cmd := m.onKey(tea.KeyMsg(key))

			if c.abort.Load() != 1 {
				t.Error("the second press did not abort the run")
			}
			if cmd == nil {
				t.Error("the view stayed open after the abort")
			}
		})
	}
}
