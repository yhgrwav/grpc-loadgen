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

	"github.com/yhgrwav/leettest/pkg/engine"
)

// ledgerShaped is the all-methods run on the ledger: the verdict said "limited
// by the run: 128 streams" while 28839 calls ran late at the generator, 24372
// waited for the connection, and 213 for a stream.
func ledgerShaped() engine.Report {
	r := oneStream()
	r.Sent, r.StreamWaited = 196788, 213
	r.NotSentLate, r.NotSentConnection, r.NotSentStream = 28839, 24372, 0
	r.NotSent = r.NotSentLate + r.NotSentConnection
	r.Connections = &engine.Connections{Open: 1, LimitAnnounced: true, FirstLimit: 128, LastLimit: 128}

	return r
}

func verdictOf(t *testing.T, r engine.Report) string {
	t.Helper()

	for _, n := range reportNotes(r) {
		if strings.HasPrefix(n, "limited by") || strings.HasPrefix(n, "the connection was not ready") {
			return n
		}
	}
	t.Fatalf("no verdict in:\n%s", strings.Join(reportNotes(r), "\n\n"))

	return ""
}

// The heading names the largest cause; every cause is listed, largest first.
func TestVerdict_NamesTheLargestCauseAndListsTheRest(t *testing.T) {
	v := verdictOf(t, ledgerShaped())

	if !strings.HasPrefix(v, "limited by the run, not the target: the generator fell behind") {
		t.Errorf("heading does not name the generator:\n%s", v)
	}
	if !strings.Contains(v, "causes, largest first: generator late 28839; connection not ready 24372; waited for a stream 213") {
		t.Errorf("causes not listed largest first:\n%s", v)
	}
	if s := shortStreamVerdict(ledgerShaped()); !strings.Contains(s, "generator") {
		t.Errorf("short verdict %q does not name the generator", s)
	}
}

// A connection not ready is the target or the network refusing it: never
// "limited by the run", which would take the blame off the target.
func TestVerdict_AConnectionNotReadyIsNotLimitedByTheRun(t *testing.T) {
	r := ledgerShaped()
	r.NotSentLate, r.NotSentConnection, r.StreamWaited = 10, 500, 0
	r.NotSent = 510
	r.Methods[0].P99WithoutStreamWait = r.Methods[0].P99

	v := verdictOf(t, r)
	if !strings.HasPrefix(v, "the connection was not ready for 500 calls") {
		t.Errorf("heading:\n%s", v)
	}
	if strings.Contains(strings.Join(reportNotes(r), "\n"), "limited by the run") {
		t.Errorf("a connection not ready reads as the run's limit:\n%s", strings.Join(reportNotes(r), "\n\n"))
	}
	if s := shortStreamVerdict(r); strings.Contains(s, "limited by") {
		t.Errorf("short verdict %q", s)
	}
}

// Equal causes: a fixed order decides — generator, stream, connection.
func TestVerdict_ATieIsBrokenByAFixedOrder(t *testing.T) {
	r := ledgerShaped()
	r.NotSentLate, r.NotSentConnection, r.StreamWaited = 100, 100, 100
	r.NotSent = 200

	v := verdictOf(t, r)
	if !strings.HasPrefix(v, "limited by the run, not the target: the generator fell behind") {
		t.Errorf("heading:\n%s", v)
	}
	if !strings.Contains(v, "causes, largest first: generator late 100; waited for a stream 100; connection not ready 100") {
		t.Errorf("tie order:\n%s", v)
	}
}

// A call that waited 950ms of its 1s before going out got 50ms from the target:
// the no-answer line says how many went out with less than half the deadline.
func TestNotes_NoAnswerLineSaysHowManyWentOutLate(t *testing.T) {
	r := oneStream()
	r.Methods[0].TimedOut, r.Methods[0].TimedOutAfterWait = 48, 30

	if text := strings.Join(reportNotes(r), "\n"); !strings.Contains(text, "30 of them went out with less than half the timeout left") {
		t.Errorf("no count of late-sent timeouts:\n%s", text)
	}
}
