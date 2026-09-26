package clock

import (
	"slices"
	"time"
)

const (
	// ticks is how many whole ticks StepOf takes the median of: about 0.25 s
	// on a 15.6 ms clock.
	ticks = 16
	// maxReads ends the measure of a clock that stopped moving: seconds of
	// time.Now calls, far past 16 ticks of any timer Windows has.
	maxReads = 1 << 26
)

// StepOf is the step of the clock now reads: the median of whole ticks it
// moved by, or 0 when it did not move at all. A clock shows whole ticks, so
// even the first move is one. A median, since a preempted read sees two ticks
// as one.
func StepOf(now func() time.Time) time.Duration {
	steps := make([]time.Duration, 0, ticks)
	prev := now()
	for range maxReads {
		t := now()
		if t.Equal(prev) {
			continue
		}
		steps = append(steps, t.Sub(prev))
		if len(steps) == ticks {
			break
		}
		prev = t
	}
	if len(steps) == 0 {
		return 0
	}
	slices.Sort(steps)

	return steps[len(steps)/2]
}
