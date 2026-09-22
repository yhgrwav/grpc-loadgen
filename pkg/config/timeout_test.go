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

package config

import (
	"errors"
	"testing"
	"time"
)

// withTimeout is a valid config whose only call carries timeoutLine.
func withTimeout(timeoutLine string) []byte {
	return []byte(`app:
  target:
    ip: localhost
    port: 50051
load:
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: 10
      duration: 1s
` + timeoutLine)
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParse_TimeoutDefaultsToTwoSeconds(t *testing.T) {
	cfg, err := Parse(withTimeout(""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Load.Calls[0].Timeout; got != DefaultTimeout || DefaultTimeout != 2*time.Second {
		t.Errorf("timeout = %v (DefaultTimeout %v), want 2s when the field is absent", got, DefaultTimeout)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParse_ExplicitTimeoutIsKept(t *testing.T) {
	cfg, err := Parse(withTimeout("      timeout: 150ms\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Load.Calls[0].Timeout; got != 150*time.Millisecond {
		t.Errorf("timeout = %v, want 150ms", got)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParse_TimeoutCannotBeSwitchedOff(t *testing.T) {
	// In an open model a call without a deadline piles up against a hung
	// target until the in-flight cap ends the run; zero is not "no timeout".
	for _, line := range []string{"      timeout: 0s\n", "      timeout: -1s\n"} {
		t.Run(line, func(t *testing.T) {
			if _, err := Parse(withTimeout(line)); !errors.Is(err, ErrInvalidTimeout) {
				t.Errorf("err = %v, want ErrInvalidTimeout", err)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParse_NameIsOptional(t *testing.T) {
	cfg, err := Parse(withTimeout(""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Name != nil {
		t.Errorf("name = %q, want nil when the field is absent", *cfg.Name)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParse_EmptyNameIsRejected(t *testing.T) {
	raw := append([]byte("name: \"\"\n"), withTimeout("")...)
	if _, err := Parse(raw); !errors.Is(err, ErrEmptyName) {
		t.Errorf("err = %v, want ErrEmptyName: an empty name names nothing in the header", err)
	}
}
