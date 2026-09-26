package clock

import (
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
