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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// decodeOnly fails unless stdout is exactly one JSON object and a newline.
func decodeOnly(t *testing.T, stdout string) map[string]any {
	t.Helper()

	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON object:\n%s", stdout)
	}
	if !strings.HasSuffix(stdout, "}\n") {
		t.Errorf("stdout does not end with the object and a newline: %q", stdout[max(0, len(stdout)-10):])
	}

	return out
}

func TestRun_JSONStdoutIsOnlyTheReport(t *testing.T) {
	res := runCLI(t.Context(), t, 10*time.Second,
		"-fake", "-output", "json", "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}

	out := decodeOnly(t, res.stdout)
	if got := out["outcome"]; got != "complete" {
		t.Errorf("outcome = %v, want complete for exit code 0", got)
	}
	if !json.Valid(bytes.TrimSpace([]byte(res.stdout))) {
		t.Errorf("stdout is not valid JSON")
	}
}

func TestRun_JSONWithNoSuccessStillParses(t *testing.T) {
	res := runCLI(t.Context(), t, 10*time.Second,
		"-fake", "-fake-fail-ratio", "1", "-output", "json",
		"-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}

	out := decodeOnly(t, res.stdout)
	method := out["methods"].([]any)[0].(map[string]any)
	if p99 := method["latency"].(map[string]any)["p99"]; p99 != nil {
		t.Errorf("latency.p99 = %v, want null when no call succeeded", p99)
	}
}

func TestRun_JSONOutcomeOfAStoppedRunIsIncomplete(t *testing.T) {
	res := runSignalled(t, "      timeout: 300ms\n", func(stops, _ chan<- struct{}) { stops <- struct{}{} },
		"-output", "json")

	if exitCode(res.err) != 3 {
		t.Fatalf("exit code %d, want 3", exitCode(res.err))
	}
	if got := decodeOnly(t, res.stdout)["outcome"]; got != "incomplete" {
		t.Errorf("outcome = %v, want incomplete for exit code 3", got)
	}
}

func TestRun_JSONRunThatNeverStartedPrintsNothing(t *testing.T) {
	addr := closedPort(t)
	res := runCLI(t.Context(), t, 10*time.Second,
		"-output", "json", "-c", writeConfig(t, addr, checkMethod, plaintext))

	if exitCode(res.err) != 1 {
		t.Fatalf("exit code %d, want 1 for an unreachable target", exitCode(res.err))
	}
	// Exit 1 from a flag the CLI does not know proves nothing about the report.
	if strings.Contains(res.stderr, "flag provided but not defined") ||
		!strings.Contains(res.err.Error(), addr) {
		t.Fatalf("err = %v, want the run to fail on the connection, not on -output", res.err)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want empty: a run that never started has no report", res.stdout)
	}
}

func TestRun_UnknownOutputFormatIsAnError(t *testing.T) {
	res := runCLI(t.Context(), t, 3*time.Second,
		"-fake", "-output", "yaml", "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))

	if exitCode(res.err) != 1 || !strings.Contains(res.err.Error(), `"yaml"`) {
		t.Errorf("err = %v, want exit code 1 naming the value \"yaml\"", res.err)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want empty", res.stdout)
	}
}

// The outcome in the JSON and the exit code are one fact: a script may read
// either and must not find them apart.
func TestOutcome_MatchesTheExitCode(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		want string
		ok   bool
	}{
		{nil, 0, "complete", true},
		{ErrInvalidRun, 2, "invalid", true},
		{ErrIncomplete, 3, "incomplete", true},
		{errors.New("no connection"), 1, "", false},
	} {
		got, ok := outcomeOf(c.err)
		if exitCode(c.err) != c.code || got != c.want || ok != c.ok {
			t.Errorf("%v: exit %d, outcome %q (%v); want exit %d, %q (%v)", c.err, exitCode(c.err), got, ok, c.code, c.want, c.ok)
		}
	}
}
