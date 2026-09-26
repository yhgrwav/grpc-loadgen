package clock

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

// ticking is a clock that reads true time t advanced by adv per read, rounded
// down to tick: what time.Now does when the OS updates it once per tick.
func ticking(tick, adv time.Duration, gaps map[int]bool) func() time.Time {
	var t time.Duration
	base := time.Unix(0, 0)

	return func() time.Time {
		t += adv
		shown := t - t%tick
		if gaps[int(shown/tick)] {
			shown -= tick // this tick was missed: the next one shows two at once
		}

		return base.Add(shown)
	}
}

func TestStep_IsTheTickOfACoarseClock(t *testing.T) {
	if got := StepOf(ticking(500*time.Microsecond, 7*time.Microsecond, nil)); got != 500*time.Microsecond {
		t.Errorf("StepOf = %v, want 500µs", got)
	}
	if got := StepOf(ticking(15625*time.Microsecond, 50*time.Microsecond, nil)); got != 15625*time.Microsecond {
		t.Errorf("StepOf = %v, want 15.625ms", got)
	}
}

// A preempted spin sees two ticks as one: the median holds, the max would not.
func TestStep_AMissedTickDoesNotDoubleIt(t *testing.T) {
	gaps := map[int]bool{3: true, 9: true}
	if got := StepOf(ticking(500*time.Microsecond, 7*time.Microsecond, gaps)); got != 500*time.Microsecond {
		t.Errorf("StepOf = %v, want 500µs", got)
	}
}

func TestStep_AFineClock(t *testing.T) {
	if got := StepOf(ticking(time.Nanosecond, 40*time.Nanosecond, nil)); got != 40*time.Nanosecond {
		t.Errorf("StepOf = %v, want 40ns: every read moved", got)
	}
}

// A clock that never moves ends the measure instead of spinning forever.
func TestStep_AFrozenClockIsZero(t *testing.T) {
	frozen := time.Unix(0, 0)
	if got := StepOf(func() time.Time { return frozen }); got != 0 {
		t.Errorf("StepOf = %v, want 0 for a clock that never moved", got)
	}
}

type fakeTimer struct {
	finest          uint32
	finestErr       error
	setErr, beginEr error
	calls           []string
}

func (f *fakeTimer) api() timerAPI {
	return timerAPI{
		finest: func() (uint32, error) { f.calls = append(f.calls, "finest"); return f.finest, f.finestErr },
		set: func(p uint32, on bool) error {
			f.calls = append(f.calls, fmt.Sprintf("set %d %v", p, on))
			if on {
				return f.setErr
			}

			return nil
		},
		begin: func(ms uint32) error { f.calls = append(f.calls, fmt.Sprintf("begin %d", ms)); return f.beginEr },
		end:   func(ms uint32) error { f.calls = append(f.calls, fmt.Sprintf("end %d", ms)); return nil },
	}
}

// Raise asks for the host's finest period, falls back to 1 ms, and restore
// undoes exactly the request that was made.
func TestRaise_AsksForTheFinestAndUndoesWhatItAsked(t *testing.T) {
	refused := errors.New("refused")
	for _, tc := range []struct {
		name   string
		timer  fakeTimer
		raised bool
		calls  []string
	}{
		{"finest period", fakeTimer{finest: 5000}, true,
			[]string{"finest", "set 5000 true", "set 5000 false"}},
		{"ntdll refuses: 1 ms", fakeTimer{finest: 5000, setErr: refused}, true,
			[]string{"finest", "set 5000 true", "begin 1", "end 1"}},
		{"no finest: 1 ms", fakeTimer{finestErr: refused}, true,
			[]string{"finest", "begin 1", "end 1"}},
		{"both refuse", fakeTimer{finest: 5000, setErr: refused, beginEr: refused}, false,
			[]string{"finest", "set 5000 true", "begin 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timer := tc.timer
			restore, raised := raiseWith(timer.api())
			restore()

			if raised != tc.raised {
				t.Errorf("raised = %v, want %v", raised, tc.raised)
			}
			if !slices.Equal(timer.calls, tc.calls) {
				t.Errorf("calls = %q, want %q", timer.calls, tc.calls)
			}
		})
	}
}

// A host without timer calls (Linux, macOS) has a fine clock: nothing to raise.
func TestRaise_NothingToRaiseIsRaised(t *testing.T) {
	restore, raised := raiseWith(timerAPI{})
	restore()
	if !raised {
		t.Error("raised = false on a host with no timer to raise")
	}
}
