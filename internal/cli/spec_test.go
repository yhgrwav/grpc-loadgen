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

package cli_test

import (
	"testing"
	"time"

	"github.com/yhgrwav/grpc-loadgen/internal/cli"
	"github.com/yhgrwav/grpc-loadgen/pkg/config"
)

func TestCallsFromConfig(t *testing.T) {
	cfg := &config.MasterConfig{
		Load: config.Load{
			Calls: []config.Call{
				{Method: "a.B/One", RPS: 800, Duration: time.Minute},
				{Method: "a.B/Two", RPS: 50, Duration: 30 * time.Second},
			},
		},
	}

	calls := cli.CallsFromConfig(cfg)

	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}

	first := calls[0]
	// The config writes a.B/One; gRPC sends /a.B/One. The slash is added here,
	// once, so the sender takes the path as given.
	if first.Method != "/a.B/One" {
		t.Errorf("method = %q, want the gRPC path /a.B/One", first.Method)
	}
	if len(first.Stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(first.Stages))
	}
	if first.Stages[0].StartRPS != 800 || first.Stages[0].TargetRPS != 800 {
		t.Errorf("stage rates = %d and %d, want 800 for both",
			first.Stages[0].StartRPS, first.Stages[0].TargetRPS)
	}
	if first.Stages[0].Duration != time.Minute {
		t.Errorf("duration = %s, want 1m", first.Stages[0].Duration)
	}
}

func named(name string) *string { return &name }

func TestServiceLabel(t *testing.T) {
	call := func(method string) config.Call { return config.Call{Method: method} }

	tests := []struct {
		name  string
		cfg   config.MasterConfig
		path  string
		label string
	}{
		{
			name:  "one service gives its short name",
			cfg:   config.MasterConfig{Load: config.Load{Calls: []config.Call{call("wallet.v1.WalletService/Get"), call("wallet.v1.WalletService/Put")}}},
			path:  "configs/loadgen.yaml",
			label: "WalletService",
		},
		{
			name:  "several services fall back to the config file name",
			cfg:   config.MasterConfig{Load: config.Load{Calls: []config.Call{call("a.v1.One/Get"), call("b.v1.Two/Get")}}},
			path:  "configs/checkout.yaml",
			label: "checkout",
		},
		{
			name:  "an explicit name wins over the service",
			cfg:   config.MasterConfig{Name: named("wallet smoke"), Load: config.Load{Calls: []config.Call{call("wallet.v1.WalletService/Get")}}},
			path:  "loadgen.yaml",
			label: "wallet smoke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cli.ServiceLabel(&tt.cfg, tt.path); got != tt.label {
				t.Errorf("ServiceLabel = %q, want %q", got, tt.label)
			}
		})
	}
}
