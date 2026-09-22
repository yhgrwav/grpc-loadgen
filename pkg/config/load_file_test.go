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

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yhgrwav/leettest/pkg/config"
)

const validYAML = `
app:
  target:
    ip: localhost
    port: 50051
load:
  warmup: 5s
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m
`

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParse(t *testing.T) {
	cfg, err := config.Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if cfg.App.Address != "localhost:50051" {
		t.Errorf("address = %q, want localhost:50051", cfg.App.Address)
	}
	if !cfg.App.UseTLS {
		t.Error("UseTLS = false, want true when tls is omitted")
	}
	if cfg.Load.Warmup != 5*time.Second {
		t.Errorf("warmup = %s, want 5s", cfg.Load.Warmup)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParseRejectsUnknownFields(t *testing.T) {
	const typo = `
app:
  target:
    ip: localhost
    port: 50051
load:
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rsp: 800
      duration: 1m
`

	if _, err := config.Parse([]byte(typo)); err == nil {
		t.Fatal("parse accepted an unknown field")
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParseRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{
			name: "no target",
			raw: `
load:
  calls:
    - method: a.B/C
      rps: 1
      duration: 1s
`,
			wantErr: config.ErrInvalidIP,
		},
		{
			name: "no calls",
			raw: `
app:
  target:
    ip: localhost
    port: 50051
`,
			wantErr: config.ErrNoCalls,
		},
		{
			name:    "broken yaml",
			raw:     "app: [",
			wantErr: config.ErrReadConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tt.raw))

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leettest.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}

	if cfg.App.Address != "localhost:50051" {
		t.Errorf("address = %q, want localhost:50051", cfg.App.Address)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestLoadFileMissing(t *testing.T) {
	_, err := config.LoadFile(filepath.Join(t.TempDir(), "absent.yaml"))

	if !errors.Is(err, config.ErrReadConfig) {
		t.Fatalf("error = %v, want %v", err, config.ErrReadConfig)
	}
}
