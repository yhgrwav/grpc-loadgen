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
	"errors"
	"path/filepath"
	"strings"

	"github.com/yhgrwav/grpc-loadgen/pkg/config"
	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

// CallsFromConfig turns the calls of a parsed config into engine calls.
func CallsFromConfig(cfg *config.MasterConfig) []engine.Call {
	calls := make([]engine.Call, 0, len(cfg.Load.Calls))

	for _, call := range cfg.Load.Calls {
		calls = append(calls, engine.Call{
			// gRPC sends /pkg.Service/Method; the config writes it without the slash.
			Method:  "/" + call.Method,
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

// displayMethod shows a method the way the config names it: the engine carries
// the gRPC path, which differs only by the leading slash.
func displayMethod(method string) string {
	return strings.TrimPrefix(method, "/")
}

// ServiceLabel is what the header calls the run: the name the config gives it;
// otherwise the one service all its methods belong to, by its short name;
// otherwise the config file's name, which is the collection name by default.
func ServiceLabel(cfg *config.MasterConfig, configPath string) string {
	if cfg.Name != nil {
		return *cfg.Name
	}

	service := ""

	for _, call := range cfg.Load.Calls {
		full, _, _ := strings.Cut(call.Method, "/")
		if service != "" && full != service {
			return strings.TrimSuffix(filepath.Base(configPath), filepath.Ext(configPath))
		}
		service = full
	}

	return service[strings.LastIndex(service, ".")+1:]
}

// ErrRequestData says a call's data does not fit its method's request message.
var ErrRequestData = errors.New("request data does not fit the method")
