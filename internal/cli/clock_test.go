package cli

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

func clockRun(step, p50 time.Duration) RunReport {
	m := engine.MethodReport{Method: "/pkg.S/M", Sent: 1000}
	if p50 > 0 {
		m.P50 = metrics.Quantile{Value: p50, Exact: true, Defined: true}
	}

	return RunReport{Report: engine.Report{Methods: []engine.MethodReport{m}}, ClockStep: step}
}

func printedRun(run RunReport) string {
	var out bytes.Buffer
	PrintReport(&out, "localhost:50051", run)

	return out.String()
}

// Every latency is a difference of two stamps, each off by up to one step.
func TestClockStep_TheNoteSaysPlusMinusAStep(t *testing.T) {
	step := 502 * time.Microsecond
	want := fmt.Sprintf("clock step %s on this host: every latency and wait is +/- %s", formatLatency(step), formatLatency(step))
	if out := printedRun(clockRun(step, 10*time.Millisecond)); !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}

	// 80.0µs prints to 0.1µs: a 5µs step is 6% of it. Linux and macOS step in
	// tens of nanoseconds, under the 1µs the note starts at.
	if out := printedRun(clockRun(time.Microsecond, 80*time.Microsecond)); !strings.Contains(out, "clock step") {
		t.Errorf("a 1µs step is not noted:\n%s", out)
	}
	if out := printedRun(clockRun(900*time.Nanosecond, 80*time.Microsecond)); strings.Contains(out, "clock step") {
		t.Errorf("a 900ns step is noted:\n%s", out)
	}
}

// A floor raised by the step changes what counts as waiting: the note names it.
func TestClockStep_TheNoteNamesARaisedWaitFloor(t *testing.T) {
	run := clockRun(502*time.Microsecond, 10*time.Millisecond)
	run.WaitFloor = 2008 * time.Microsecond
	run.ClockStepBefore = 502 * time.Microsecond
	want := "a wait counts from " + formatLatency(run.WaitFloor)
	if out := printedRun(run); !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}

	run.WaitFloor = engine.StreamWaitFloor
	if out := printedRun(run); strings.Contains(out, "a wait counts from") {
		t.Errorf("the default floor is named:\n%s", out)
	}
}

// The floor was set from the step before the run: a step that grew by a
// quarter or more left it too low, and nothing else in the report shows that.
func TestClockStep_AStepThatGrewIsAnInvalidRun(t *testing.T) {
	for _, tc := range []struct {
		before, after time.Duration
		invalid       bool
	}{
		{500 * time.Microsecond, 15625 * time.Microsecond, true},
		{500 * time.Microsecond, 625 * time.Microsecond, true},
		{500 * time.Microsecond, 521 * time.Microsecond, false},
	} {
		run := clockRun(max(tc.before, tc.after), 100*time.Millisecond)
		run.ClockStepBefore = tc.before
		name := fmt.Sprintf("%v → %v", tc.before, tc.after)

		if got := ClockTooCoarse(run); got != tc.invalid {
			t.Errorf("%s: ClockTooCoarse = %v, want %v", name, got, tc.invalid)
		}
		want := fmt.Sprintf("clock step changed during the run: %s before, %s after", formatLatency(tc.before), formatLatency(tc.after))
		if got := strings.Contains(printedRun(run), want); got != tc.invalid {
			t.Errorf("%s: %q printed = %v, want %v", name, want, got, tc.invalid)
		}
	}
}

func TestClockStep_TheFloorSaysWhichMeasureItCameFrom(t *testing.T) {
	run := clockRun(521*time.Microsecond, 10*time.Millisecond)
	run.ClockStepBefore = 500 * time.Microsecond
	run.WaitFloor = 2 * time.Millisecond
	want := "a wait counts from " + formatLatency(run.WaitFloor) + ", four steps of the " + formatLatency(run.ClockStepBefore) + " before the run"
	if out := printedRun(run); !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}
}

// cmd.exe in code page 866 prints a UTF-8 "±" as two garbage characters.
func TestClockStep_NotesAreASCII(t *testing.T) {
	run := clockRun(15625*time.Microsecond, 2*time.Millisecond)
	run.ClockStepBefore = 500 * time.Microsecond
	run.WaitFloor = 2 * time.Millisecond
	run.TimerNotRaised = true
	for _, note := range runNotes(run) {
		if !isASCII(note) {
			t.Errorf("not ASCII: %q", note)
		}
	}
}

func TestClockStep_ATimerNotRaisedIsNoted(t *testing.T) {
	run := clockRun(502*time.Microsecond, 10*time.Millisecond)
	run.TimerNotRaised = true
	if out := printedRun(run); !strings.Contains(out, "timer resolution not raised") {
		t.Errorf("no note on the timer:\n%s", out)
	}
	if out := printedRun(clockRun(502*time.Microsecond, 10*time.Millisecond)); strings.Contains(out, "timer resolution") {
		t.Errorf("a raised timer is noted:\n%s", out)
	}
}

func TestClockStep_JSONAlwaysHasIt(t *testing.T) {
	for step, want := range map[time.Duration]float64{502300 * time.Nanosecond: 502300, 40 * time.Nanosecond: 40} {
		out := writeJSON(t, JSONRun{Target: "t", Outcome: OutcomeComplete, Run: clockRun(step, time.Millisecond)})
		if v := field(t, out, "clock_step_ns"); v != want {
			t.Errorf("clock_step_ns = %v, want %v", v, want)
		}
	}
}

// A step over a quarter of a method's p50 leaves that p50 off by over 25%:
// the run is invalid. A method without a p50 measured nothing to judge.
func TestClockStep_OverAQuarterOfP50IsAnInvalidRun(t *testing.T) {
	for _, tc := range []struct {
		step, p50 time.Duration
		invalid   bool
	}{
		{500 * time.Microsecond, 2 * time.Millisecond, false},
		{502 * time.Microsecond, 2 * time.Millisecond, true},
		{15625 * time.Microsecond, 62500 * time.Microsecond, false},
		{15625 * time.Microsecond, 60 * time.Millisecond, true},
		{15625 * time.Microsecond, 0, false},
	} {
		run := clockRun(tc.step, tc.p50)
		name := fmt.Sprintf("step %v p50 %v", tc.step, tc.p50)

		if got := ClockTooCoarse(run); got != tc.invalid {
			t.Errorf("%s: ClockTooCoarse = %v, want %v", name, got, tc.invalid)
		}
		note := strings.Contains(printedRun(run), "invalid run: the clock step")
		if note != tc.invalid {
			t.Errorf("%s: invalid-run note printed = %v, want %v", name, note, tc.invalid)
		}
		out := writeJSON(t, JSONRun{Target: "t", Outcome: OutcomeComplete, Run: run})
		reasons := field(t, out, "invalid_reasons").([]any)
		if got := slices.Contains(reasons, any("clock_step")); got != tc.invalid {
			t.Errorf("%s: clock_step in invalid_reasons %v = %v, want %v", name, reasons, got, tc.invalid)
		}
	}
}
