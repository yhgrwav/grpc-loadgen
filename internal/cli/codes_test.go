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

func printCodes(codes []engine.CodeCount, failed int) string {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: engine.Report{
		Duration: time.Second, Sent: 100, Failed: failed,
		Methods: []engine.MethodReport{{Method: "a.B/One", Sent: 100, Failed: failed, RPS: 100, FailureCodes: codes}},
	}})

	return out.String()
}

// The category says whose fault a failure is; the code is what a developer
// greps the target's logs for. Both are in the report.
func TestPrintReport_NamesTheCodesOfFailedCalls(t *testing.T) {
	text := printCodes([]engine.CodeCount{{Code: "Unavailable", Count: 12, FromTarget: true}, {Code: "DeadlineExceeded", Count: 3, FromTarget: true}}, 15)

	if !strings.Contains(text, "a.B/One codes sent by the target: Unavailable 12, DeadlineExceeded 3") {
		t.Errorf("no codes line under the method:\n%s", text)
	}
}

// A method that did not fail prints no codes line.
func TestPrintReport_NoFailuresNoCodesLine(t *testing.T) {
	if text := printCodes(nil, 0); strings.Contains(text, "codes:") {
		t.Errorf("a codes line with nothing failed:\n%s", text)
	}
}

// Codes the client set are never printed as the target's answer.
func TestPrintReport_SeparatesTheTargetsCodesFromTheClients(t *testing.T) {
	text := printCodes([]engine.CodeCount{
		{Code: "Unavailable", Count: 12, FromTarget: true},
		{Code: "DeadlineExceeded", Count: 3, FromTarget: false},
	}, 15)

	for _, want := range []string{
		"a.B/One codes sent by the target: Unavailable 12",
		"a.B/One codes set by the client: DeadlineExceeded 3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

// Only client-made codes: no line claims the target sent anything.
func TestPrintReport_OnlyClientCodesClaimNothingOfTheTarget(t *testing.T) {
	text := printCodes([]engine.CodeCount{{Code: "DeadlineExceeded", Count: 3}}, 3)

	if strings.Contains(text, "sent by the target") {
		t.Errorf("a target line for codes the client made:\n%s", text)
	}
	if !strings.Contains(text, "set by the client: DeadlineExceeded 3") {
		t.Errorf("no client line:\n%s", text)
	}
}
