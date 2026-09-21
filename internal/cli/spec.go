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

package cli

import (
	"github.com/yhgrwav/grpc-loadgen/pkg/config"
	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

// CallsFromConfig turns the calls of a parsed config into engine calls.
func CallsFromConfig(cfg *config.MasterConfig) []engine.Call {
	calls := make([]engine.Call, 0, len(cfg.Load.Calls))

	for _, call := range cfg.Load.Calls {
		calls = append(calls, engine.Call{
			Method:  call.Method,
			Timeout: call.Timeout,
			Stages: []engine.Stage{{
				StartRPS:  call.RPS,
				TargetRPS: call.RPS,
				Duration:  call.Duration,
			}},
		})
	}

	return calls
}
