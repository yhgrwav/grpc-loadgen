package main

import (
	"os"
	"slices"
	"testing"
	"time"
)

// The end-to-end runs here check what the report says about the target on a
// loopback target answering in microseconds; on a 0.5 ms Windows clock every
// one would be invalid. The clock rule has its own tests.
func TestMain(m *testing.M) {
	clockStep = func() time.Duration { return 0 }
	os.Exit(m.Run())
}

// The whole path: the measured step reaches the report, not only runResult.
// A 15.625 ms step against the fake target's 25 ms p50 is over a quarter.
func TestRun_ACoarseClockIsExit2WithClockStepInJSON(t *testing.T) {
	saved := clockStep
	clockStep = func() time.Duration { return 15625 * time.Microsecond }
	t.Cleanup(func() { clockStep = saved })

	res := runCLI(t.Context(), t, 10*time.Second,
		"-fake", "-output", "json", "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
	if code := exitCode(res.err); code != 2 {
		t.Errorf("exit code %d (%v), want 2", code, res.err)
	}

	out := decodeOnly(t, res.stdout)
	reasons, _ := out["invalid_reasons"].([]any)
	if !slices.Contains(reasons, any("clock_step")) {
		t.Errorf("invalid_reasons = %v, want clock_step", reasons)
	}
}
