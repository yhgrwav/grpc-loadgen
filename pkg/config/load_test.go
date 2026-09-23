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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/yhgrwav/leettest/pkg/config"
)

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestLoadParsesDurations(t *testing.T) {
	const raw = `
app:
  target:
    ip: localhost
    port: 50051
  tls: false
load:
  warmup: 10s
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m
`

	var cfg config.MasterConfig
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Load.Warmup != 10*time.Second {
		t.Errorf("warmup = %s, want 10s", cfg.Load.Warmup)
	}
	if len(cfg.Load.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(cfg.Load.Calls))
	}
	if got := cfg.Load.Calls[0].Duration; got != time.Minute {
		t.Errorf("duration = %s, want 1m", got)
	}
	if got := cfg.Load.Calls[0].RPS; got != 800 {
		t.Errorf("rps = %d, want 800", got)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestCallValidate(t *testing.T) {
	valid := config.Call{
		Method:   "wallet.v1.WalletService/GetBalance",
		RPS:      800,
		Duration: time.Minute,
	}

	tests := []struct {
		name    string
		mutate  func(*config.Call)
		wantErr error
	}{
		{name: "valid", mutate: func(*config.Call) {}},
		{name: "empty method", mutate: func(c *config.Call) { c.Method = "" }, wantErr: config.ErrInvalidMethod},
		{name: "method without service", mutate: func(c *config.Call) { c.Method = "GetBalance" }, wantErr: config.ErrInvalidMethod},
		{name: "method without package", mutate: func(c *config.Call) { c.Method = "WalletService/GetBalance" }, wantErr: config.ErrInvalidMethod},
		{name: "method without name", mutate: func(c *config.Call) { c.Method = "wallet.v1.WalletService/" }, wantErr: config.ErrInvalidMethod},
		{name: "zero rps", mutate: func(c *config.Call) { c.RPS = 0 }, wantErr: config.ErrInvalidRPS},
		{name: "negative rps", mutate: func(c *config.Call) { c.RPS = -1 }, wantErr: config.ErrInvalidRPS},
		{name: "zero duration", mutate: func(c *config.Call) { c.Duration = 0 }, wantErr: config.ErrInvalidDuration},
		{name: "negative duration", mutate: func(c *config.Call) { c.Duration = -time.Second }, wantErr: config.ErrInvalidDuration},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := valid
			tt.mutate(&call)

			err := call.Validate()

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestCallValidateReportsEveryProblem(t *testing.T) {
	call := config.Call{}

	err := call.Validate()

	for _, want := range []error{config.ErrInvalidMethod, config.ErrInvalidRPS, config.ErrInvalidDuration} {
		if !errors.Is(err, want) {
			t.Errorf("error %v does not report %v", err, want)
		}
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestLoadValidate(t *testing.T) {
	call := config.Call{
		Method:   "wallet.v1.WalletService/GetBalance",
		RPS:      800,
		Duration: time.Minute,
	}

	tests := []struct {
		name    string
		load    config.Load
		wantErr error
	}{
		{name: "valid", load: config.Load{Calls: []config.Call{call}}},
		{name: "warmup is optional", load: config.Load{Warmup: 0, Calls: []config.Call{call}}},
		{name: "no calls", load: config.Load{}, wantErr: config.ErrNoCalls},
		{name: "negative warmup", load: config.Load{Warmup: -time.Second, Calls: []config.Call{call}}, wantErr: config.ErrInvalidWarmup},
		{name: "broken call", load: config.Load{Calls: []config.Call{{}}}, wantErr: config.ErrInvalidMethod},
		{name: "method in two calls", load: config.Load{Calls: []config.Call{call, call}}, wantErr: config.ErrDuplicateMethod},
		// The ban compares raw strings, which is the report's key only while the
		// config takes one spelling per method: another spelling must be refused,
		// not treated as a second method.
		{name: "method again with a leading slash", load: config.Load{Calls: []config.Call{call, withMethod(call, "/"+call.Method)}}, wantErr: config.ErrInvalidMethod},
		{name: "method again with a dot for the slash", load: config.Load{Calls: []config.Call{call, withMethod(call, "wallet.v1.WalletService.GetBalance")}}, wantErr: config.ErrInvalidMethod},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.load.Validate()

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func withMethod(call config.Call, method string) config.Call {
	call.Method = method
	return call
}

const oneCall = `
app:
  target:
    ip: localhost
    port: 50051
load:
  warmup: %s
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: %s
      duration: 10s
`

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParseRejectsFractionalRPS(t *testing.T) {
	for _, rps := range []string{"10.5", "10.0", "1e3", "0.9"} {
		t.Run(rps, func(t *testing.T) {
			_, err := config.Parse(fmt.Appendf(nil, oneCall, "0s", rps))

			if !errors.Is(err, config.ErrFractionalRPS) {
				t.Fatalf("error = %v, want %v: a silently truncated rate is load nobody asked for", err, config.ErrFractionalRPS)
			}
			if !strings.Contains(err.Error(), rps) {
				t.Errorf("error %q does not quote the value %q", err, rps)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestParseKeepsWholeRPS(t *testing.T) {
	cfg, err := config.Parse(fmt.Appendf(nil, oneCall, "0s", "800"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Load.Calls[0].RPS; got != 800 {
		t.Errorf("rps = %v, want 800", got)
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestValidateRejectsWarmupThatLeavesNothingMeasured(t *testing.T) {
	for _, warmup := range []string{"10s", "11s"} {
		t.Run(warmup, func(t *testing.T) {
			_, err := config.Parse(fmt.Appendf(nil, oneCall, warmup, "10"))

			if !errors.Is(err, config.ErrWarmupCoversTheCall) {
				t.Fatalf("error = %v, want %v", err, config.ErrWarmupCoversTheCall)
			}
			if !strings.Contains(err.Error(), "wallet.v1.WalletService/GetBalance") {
				t.Errorf("error %q does not name the call it covers", err)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestValidateNamesTheMethodOfABadCall(t *testing.T) {
	_, err := config.Parse(fmt.Appendf(nil, oneCall, "0s", "0"))

	if !errors.Is(err, config.ErrInvalidRPS) {
		t.Fatalf("error = %v, want %v", err, config.ErrInvalidRPS)
	}
	if !strings.Contains(err.Error(), "wallet.v1.WalletService/GetBalance") {
		t.Errorf("error %q says which call is wrong only by its number", err)
	}
}
