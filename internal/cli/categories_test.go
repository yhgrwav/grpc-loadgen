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
	"github.com/yhgrwav/leettest/pkg/metrics"
)

func refusals(n int, p50 time.Duration) engine.RefusalLatency {
	q := metrics.Quantile{Value: p50, Exact: true, Defined: true}
	return engine.RefusalLatency{Count: n, P50: q, P90: q, P95: q, P99: q, Max: q}
}

func categoryReport() engine.Report {
	return engine.Report{
		Duration: time.Second, Sent: 100, Failed: 40,
		Methods: []engine.MethodReport{{
			Method: "a.B/One", Sent: 100, Failed: 40,
			Rejected:    refusals(4, time.Millisecond),
			Overload:    refusals(11, 2*time.Millisecond),
			Failure:     refusals(12, 3*time.Millisecond),
			BadResponse: refusals(13, 4*time.Millisecond),
		}},
	}
}

// Each kind of refusal has its own row, named as in the output contract.
func TestPrintReport_EveryRefusalKindHasItsOwnRow(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: categoryReport()})
	text := out.String()

	for label, count := range map[string]string{"request error": "4", "overload": "11", "failure": "12", "bad response": "13"} {
		found := false
		for line := range strings.Lines(text) {
			fields := strings.Fields(strings.TrimPrefix(line, "  "+label))
			if strings.HasPrefix(line, "  "+label) && len(fields) > 0 && fields[0] == count {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q row with %s calls:\n%s", label, count, text)
		}
	}
	for _, old := range []string{"error status", "rejected"} {
		if strings.Contains(text, "  "+old) {
			t.Errorf("the old %q row is still printed:\n%s", old, text)
		}
	}
}

// UNAVAILABLE is also a proxy with no live upstream, not only a target out of
// capacity: the overload row says what the status says, not what the target is.
func TestPrintReport_TheOverloadRowSaysWhatTheStatusSays(t *testing.T) {
	var out strings.Builder
	PrintReport(&out, "localhost:50051", RunReport{Report: categoryReport()})
	text := strings.Join(strings.Fields(out.String()), " ")

	if !strings.Contains(text, "status says overloaded or unavailable") {
		t.Errorf("the overload row is not explained as what the status says:\n%s", out.String())
	}
	if strings.Contains(text, "target is overloaded") {
		t.Errorf("the report claims the target is overloaded:\n%s", out.String())
	}
}

// A method whose every call the client could not send: invalid, and said so
// in its own words rather than as a request the target will not serve.
func TestNotes_EveryCallTheClientCouldNotSend(t *testing.T) {
	report := engine.Report{
		Duration: time.Second, Sent: 10, Failed: 10, RequestRejected: true,
		Methods: []engine.MethodReport{{Method: "a.B/One", Sent: 10, Failed: 10, ClientError: 10}},
	}

	text := strings.Join(strings.Fields(strings.Join(reportNotes(report, ""), " ")), " ")
	if !strings.Contains(text, "the client could not send any call of a.B/One") {
		t.Errorf("no note that the client sent nothing:\n%s", text)
	}
}
