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
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// -version answers without a config and without touching the network.
func TestRun_VersionPrintsAndExitsWithoutAConfig(t *testing.T) {
	res := runCLI(t.Context(), t, 5*time.Second, "-version")
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if !strings.HasPrefix(res.stdout, "leettest ") || !strings.HasSuffix(res.stdout, "\n") {
		t.Errorf("stdout = %q, want one line \"leettest <version>\"", res.stdout)
	}
	if strings.Count(res.stdout, "\n") != 1 {
		t.Errorf("stdout = %q, want exactly one line", res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing", res.stderr)
	}
}

// -version is answered before any check: a config path that does not exist,
// exit code 1 without the flag, does not stop it.
func TestRun_VersionIsCheckedBeforeTheConfig(t *testing.T) {
	res := runCLI(t.Context(), t, 5*time.Second, "-version", "-c", filepath.Join(t.TempDir(), "missing.yaml"))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if !strings.HasPrefix(res.stdout, "leettest ") {
		t.Errorf("stdout = %q, want \"leettest <version>\"", res.stdout)
	}
}

// -version wins over everything else on the line: asked for a version, the
// user gets it, not a run.
func TestRun_VersionWithAConfigDoesNotRun(t *testing.T) {
	target := startTarget(t)

	res := runCLI(t.Context(), t, 5*time.Second, "-version", "-c", writeConfig(t, target.addr, checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if got := target.health.calls.Load(); got != 0 {
		t.Errorf("target received %d calls, want none", got)
	}
}

// Ground: boundary — which source names the version: the release build's, then Go's module
// version, then the commit; a test binary carries none of them, so each is set here.
func TestVersionString(t *testing.T) {
	info := func(main string, settings ...string) *debug.BuildInfo {
		bi := &debug.BuildInfo{Main: debug.Module{Version: main}}
		for i := 0; i+1 < len(settings); i += 2 {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}

		return bi
	}

	cases := []struct {
		name    string
		release string
		info    *debug.BuildInfo
		want    string
	}{
		{"release build", "v0.1.0", info("(devel)"), "v0.1.0"},
		{"release build wins over go install", "v0.1.0", info("v0.0.9"), "v0.1.0"},
		{"go install @version", "", info("v0.1.0"), "v0.1.0"},
		{"from a checkout", "", info("(devel)", "vcs.revision", "5d9bb17aa0c1d2e3f4", "vcs.modified", "false"), "devel 5d9bb17aa0c1"},
		{"from a modified checkout", "", info("(devel)", "vcs.revision", "5d9bb17aa0c1d2e3f4", "vcs.modified", "true"), "devel 5d9bb17aa0c1+dirty"},
		{"no build info", "", nil, "devel"},
		{"no revision", "", info("(devel)"), "devel"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionString(c.release, c.info); got != c.want {
				t.Errorf("versionString = %q, want %q", got, c.want)
			}
		})
	}
}
