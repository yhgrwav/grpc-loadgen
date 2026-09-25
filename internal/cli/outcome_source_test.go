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
	"strings"
	"testing"
	"time"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// An error status may come from a proxy in front of the target: the row and its
// note must not say the target refused. nginx's 502 stub is grpc-status 14.
func TestPrintReport_AnErrorStatusIsNotPinnedOnTheTarget(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: time.Second, Sent: 100, Failed: 100,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 100, Failed: 100, RPS: 100,
			Overload:     engine.RefusalLatency{Count: 100, P50: exact(1), P99: exact(2)},
			FailureCodes: []engine.CodeCount{{Code: "Unavailable", Count: 100, FromTarget: true}},
		}},
	}})
	text := out.String()

	if strings.Contains(text, "refused") || strings.Contains(text, "the target took to say no") {
		t.Errorf("an error status reads as the target's refusal:\n%s", text)
	}
	for _, want := range []string{"overload", "from the target or a proxy in front of it"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

// Cut-off calls get a line of their own that says they reached the other end,
// and may have been processed; they are not in "never reached the target".
func TestPrintReport_CutOffCallsSayTheyReachedTheOtherEnd(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: time.Second, Sent: 100, Failed: 7,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 100, Failed: 7, RPS: 100, CutOff: 7,
			FailureCodes: []engine.CodeCount{{Code: "Internal", Count: 7}},
		}},
	}})
	text := out.String()

	if !strings.Contains(text, "7 calls were cut off after going out") || !strings.Contains(text, "may have processed them") {
		t.Errorf("no cut-off line:\n%s", text)
	}
	if strings.Contains(text, "never reached the target") {
		t.Errorf("cut-off calls reported as never reaching the target:\n%s", text)
	}
}

// A request too large may be refused by a proxy in front of the target (Envoy
// has its own limit): the "rejected" note must not say the target refused it.
func TestPrintReport_ARejectedSizeIsNotPinnedOnTheTarget(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: time.Second, Sent: 100, Failed: 100, RequestRejected: true,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 100, Failed: 100, RPS: 100,
			Rejected:     engine.RefusalLatency{Count: 100, P50: exact(1), P99: exact(2)},
			FailureCodes: []engine.CodeCount{{Code: "ResourceExhausted", Count: 100, FromTarget: true}},
		}},
	}})
	text := out.String()

	if strings.Contains(text, "the target refused") || strings.Contains(text, "request the target") {
		t.Errorf("a size refusal reads as the target's:\n%s", text)
	}
	if !strings.Contains(text, "a request refused as larger than accepted, by the target or a proxy in front of it") {
		t.Errorf("the note does not say who may have refused it:\n%s", text)
	}
}
