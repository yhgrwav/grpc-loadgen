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
	"fmt"
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

func TestStopper_StageFollowsThePresses(t *testing.T) {
	for _, tt := range []struct {
		presses int
		want    StopStage
	}{
		{0, StageNone},
		{1, StageStop},
		{2, StageAbort},
		{3, StageExit},
		{4, StageExit},
	} {
		t.Run(fmt.Sprint(tt.presses), func(t *testing.T) {
			c := newStopCalls()
			s := c.stopper(time.Hour)

			for range tt.presses {
				s.Press()
			}

			got := s.Stage()
			if got != tt.want {
				t.Errorf("Stage() = %v after %d presses, want %v", got, tt.presses, tt.want)
			}
			if stopping := s.Stopping(); stopping != (tt.want > StageNone) {
				t.Errorf("Stopping() = %v at stage %v", stopping, got)
			}
		})
	}
}

func TestStopper_StageAfterTerminateIsAbort(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Abort()

	if got := s.Stage(); got != StageAbort {
		t.Errorf("Stage() = %v after SIGTERM, want StageAbort: the view must not offer an abort again", got)
	}
}

func TestStopper_StageKeepsTheAbortAfterFinish(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Press()
	s.Press()
	s.Finish()

	if got := s.Stage(); got != StageAbort {
		t.Errorf("Stage() = %v after the report is out, want StageAbort", got)
	}
}

func TestStopper_StageIsReadWhileTheSignalArrives(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	done := make(chan struct{})
	go func() {
		defer close(done)

		s.Abort()
	}()

	for range 1000 {
		s.Stage()
	}
	<-done

	if got := s.Stage(); got != StageAbort {
		t.Errorf("Stage() = %v after the signal, want StageAbort", got)
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

func TestStopper_AbortSkipsTheGentleStop(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	if stage := s.Abort(); stage != StageAbort {
		t.Fatalf("stage = %v, want StageAbort", stage)
	}
	if c.stop.Load() != 0 || c.abort.Load() != 1 || c.exit.Load() != 0 {
		t.Errorf("stop/abort/exit = %d/%d/%d, want 0/1/0", c.stop.Load(), c.abort.Load(), c.exit.Load())
	}
}

func TestStopper_AbortAfterGentleStopAborts(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Press()
	if stage := s.Abort(); stage != StageAbort {
		t.Fatalf("stage = %v, want StageAbort", stage)
	}
}

func TestStopper_PressAfterTerminateExits(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Abort()
	if stage := s.Press(); stage != StageExit {
		t.Fatalf("stage = %v, want StageExit", stage)
	}
}

func TestStopper_AbortDuringAbortDoesNothing(t *testing.T) {
	for name, start := range map[string]func(*Stopper){
		"two presses": func(s *Stopper) { s.Press(); s.Press() },
		"terminate":   func(s *Stopper) { s.Abort() },
	} {
		t.Run(name, func(t *testing.T) {
			c := newStopCalls()
			s := c.stopper(time.Hour)

			start(s)
			if stage := s.Abort(); stage != StageNone {
				t.Fatalf("stage = %v, want StageNone: SIGTERM must not kill the report being built", stage)
			}
			if c.abort.Load() != 1 || c.exit.Load() != 0 {
				t.Errorf("abort/exit = %d/%d, want 1/0", c.abort.Load(), c.exit.Load())
			}
		})
	}
}

func TestStopper_AbortThatHangsAfterTerminateExitsAfterGrace(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(10 * time.Millisecond)

	s.Abort()
	select {
	case <-c.exited:
	case <-time.After(time.Second):
		t.Fatal("a hung abort did not exit after grace")
	}
}

func TestStopper_AbortAfterFinishDoesNothing(t *testing.T) {
	c := newStopCalls()
	s := c.stopper(time.Hour)

	s.Finish()
	if stage := s.Abort(); stage != StageNone {
		t.Fatalf("stage = %v, want StageNone", stage)
	}
}

// Ground: contract — once the run has returned, the view shows the final
// screen and the report is only waiting for it to close. A SIGTERM then must
// close it, so the report is printed and the code follows the result; before,
// it armed the grace timer and left without a report, code 143.
func TestStopper_SignalOnTheFinalScreenClosesItAndKeepsTheReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal func(*Stopper) StopStage
	}{
		{"SIGTERM", (*Stopper).Abort},
		{"Ctrl+C as a signal, twice", func(s *Stopper) StopStage { s.Press(); return s.Press() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newStopCalls()
			s := c.stopper(50 * time.Millisecond)
			var left atomic.Int32
			s.Returned(func() { left.Add(1) })

			tc.signal(s)

			select {
			case <-c.exited:
				t.Fatal("exit without a report fired on the final screen")
			case <-time.After(200 * time.Millisecond): // 4x the grace: the timer had its chance
			}
			if left.Load() == 0 || c.abort.Load() != 0 || c.stop.Load() != 0 {
				t.Errorf("leave/stop/abort = %d/%d/%d, want the screen closed and nothing stopped",
					left.Load(), c.stop.Load(), c.abort.Load())
			}
		})
	}
}

// Ground: contract — Ctrl+C as a key on the final screen does what q does.
func TestFinalScreenCtrlCIsQ(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		m := testModel(t)
		m.done = true
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		if key == "ctrl+c" {
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}
		if _, cmd := m.Update(msg); cmd == nil || fmt.Sprint(cmd()) != fmt.Sprint(tea.Quit()) {
			t.Errorf("%s on the final screen does not quit", key)
		}
	}
}
