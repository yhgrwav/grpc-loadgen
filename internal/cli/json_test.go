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
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

// schemaLines walks the JSON types, not a run's output: a pointer is the only
// thing that may be null, whatever one run happened to produce.
func schemaLines(t reflect.Type, prefix string, out *[]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name

		ft, nullable := f.Type, false
		if ft.Kind() == reflect.Pointer {
			ft, nullable = ft.Elem(), true
		}

		suffix := ""
		if nullable {
			suffix = "?"
		}

		switch ft.Kind() {
		case reflect.Struct:
			*out = append(*out, path+" object"+suffix)
			schemaLines(ft, path+".", out)
		case reflect.Slice:
			elem := ft.Elem()
			if elem.Kind() == reflect.Struct {
				*out = append(*out, path+" []object"+suffix)
				schemaLines(elem, path+"[].", out)
			} else {
				*out = append(*out, path+" []"+kindName(elem.Kind())+suffix)
			}
		default:
			*out = append(*out, path+" "+kindName(ft.Kind())+suffix)
		}
	}
}

func kindName(k reflect.Kind) string {
	switch k {
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float64:
		return "float"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	default:
		return k.String()
	}
}

func TestJSONSchema_MatchesVersion1(t *testing.T) {
	want, err := os.ReadFile("testdata/schema_v1.txt")
	if err != nil {
		t.Fatalf("read golden schema: %v", err)
	}

	var got []string
	schemaLines(reflect.TypeFor[JSONReport](), "", &got)

	if g, w := strings.Join(got, "\n"), strings.TrimSpace(string(want)); g != w {
		t.Errorf("JSON types do not match schema v1: a rename, removal or type change needs schema_version 2.\ngot:\n%s\n\nwant:\n%s", g, w)
	}
	if JSONSchemaVersion != 1 {
		t.Errorf("JSONSchemaVersion = %d, want 1 while the types match schema_v1.txt", JSONSchemaVersion)
	}
}

func writeJSON(t *testing.T, run JSONRun) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	if err := WriteJSON(&buf, run); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, buf.String())
	}

	return out
}

func jsonRun(report engine.Report) JSONRun {
	return JSONRun{
		Target:    "localhost:50051",
		Version:   "v0.0.0-test",
		Outcome:   OutcomeComplete,
		StartedAt: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
		Run:       RunReport{Report: report},
	}
}

func field(t *testing.T, v any, path ...string) any {
	t.Helper()

	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%v: not an object at %q", path, p)
		}
		if v, ok = m[p]; !ok {
			t.Fatalf("%v: no field %q", path, p)
		}
	}

	return v
}

func TestJSON_EmptyListsAreArraysNotNull(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{Method: "/pkg.S/M"}}}))

	for _, path := range [][]string{{"unchecked"}} {
		if _, ok := field(t, out, path...).([]any); !ok {
			t.Errorf("%v = %v, want []", path, field(t, out, path...))
		}
	}

	method := field(t, out, "methods").([]any)[0]
	for _, name := range []string{"failure_codes", "seconds"} {
		if _, ok := field(t, method, name).([]any); !ok {
			t.Errorf("methods[0].%s = %v, want []", name, field(t, method, name))
		}
	}

	none := writeJSON(t, jsonRun(engine.Report{}))
	if _, ok := field(t, none, "methods").([]any); !ok {
		t.Errorf("methods = %v, want []", field(t, none, "methods"))
	}
}

func TestJSON_UndefinedPercentileIsNull(t *testing.T) {
	// Nothing succeeded: every percentile has no observations.
	out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{Method: "/pkg.S/M", Sent: 10, Failed: 10}}}))

	method := field(t, out, "methods").([]any)[0]
	for _, name := range []string{"min", "p50", "p90", "p95", "p99", "max"} {
		if v := field(t, method, "latency", name); v != nil {
			t.Errorf("latency.%s = %v, want null", name, v)
		}
	}
	if v := field(t, method, "p99_without_client_waits"); v != nil {
		t.Errorf("p99_without_client_waits = %v, want null", v)
	}
	if v := field(t, out, "start_lag", "p99"); v != nil {
		t.Errorf("start_lag.p99 = %v, want null", v)
	}
}

func TestJSON_PercentileIsMicrosecondsWithItsBound(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{
		Method: "/pkg.S/M",
		P50:    metrics.Quantile{Value: 1234567 * time.Nanosecond, Exact: true, Defined: true},
		P99:    metrics.Quantile{Value: 5 * time.Second, Exact: false, Defined: true},
	}}}))

	method := field(t, out, "methods").([]any)[0]
	// JSON numbers decode as float64; microseconds are whole, truncated.
	if us, bound := field(t, method, "latency", "p50", "us"), field(t, method, "latency", "p50", "lower_bound"); us != 1234.0 || bound != false {
		t.Errorf("p50 = {us: %v, lower_bound: %v}, want {1234, false}", us, bound)
	}
	if us, bound := field(t, method, "latency", "p99", "us"), field(t, method, "latency", "p99", "lower_bound"); us != 5e6 || bound != true {
		t.Errorf("p99 = {us: %v, lower_bound: %v}, want {5000000, true}: a tail past the timeout is known only from below", us, bound)
	}
}

func TestJSON_FailureCodesAreCanonicalNames(t *testing.T) {
	// Names from grpc/grpc doc/statuscodes.md.
	want := map[codes.Code]string{
		codes.OK: "OK", codes.Canceled: "CANCELLED", codes.Unknown: "UNKNOWN",
		codes.InvalidArgument: "INVALID_ARGUMENT", codes.DeadlineExceeded: "DEADLINE_EXCEEDED",
		codes.NotFound: "NOT_FOUND", codes.AlreadyExists: "ALREADY_EXISTS",
		codes.PermissionDenied: "PERMISSION_DENIED", codes.ResourceExhausted: "RESOURCE_EXHAUSTED",
		codes.FailedPrecondition: "FAILED_PRECONDITION", codes.Aborted: "ABORTED",
		codes.OutOfRange: "OUT_OF_RANGE", codes.Unimplemented: "UNIMPLEMENTED",
		codes.Internal: "INTERNAL", codes.Unavailable: "UNAVAILABLE", codes.DataLoss: "DATA_LOSS",
		codes.Unauthenticated: "UNAUTHENTICATED",
	}

	for code, name := range want {
		out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{
			Method:       "/pkg.S/M",
			FailureCodes: []engine.CodeCount{{Code: code.String(), Count: 3, FromTarget: true}},
		}}}))

		entry := field(t, field(t, out, "methods").([]any)[0], "failure_codes").([]any)[0]
		if got := field(t, entry, "code"); got != name {
			t.Errorf("code %s written as %v, want %q", code.String(), got, name)
		}
	}
}

func TestJSON_UnknownCodePassesThrough(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{
		Method:       "/pkg.S/M",
		FailureCodes: []engine.CodeCount{{Code: "Code(99)", Count: 1}},
	}}}))

	entry := field(t, field(t, out, "methods").([]any)[0], "failure_codes").([]any)[0]
	if got := field(t, entry, "code"); got != "Code(99)" {
		t.Errorf("code = %v, want it passed through as the transport gave it", got)
	}
}

func TestJSON_WarmupSecondsAreMarked(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{
		Warmup:  2 * time.Second,
		Methods: []engine.MethodReport{{Method: "/pkg.S/M", Seconds: make([]engine.Second, 4)}},
	}))

	seconds := field(t, field(t, out, "methods").([]any)[0], "seconds").([]any)
	for i, want := range []bool{true, true, false, false} {
		if got := field(t, seconds[i], "warmup"); got != want {
			t.Errorf("seconds[%d].warmup = %v, want %v", i, got, want)
		}
	}
}

func TestJSON_HeaderFields(t *testing.T) {
	run := jsonRun(engine.Report{Duration: 3 * time.Second, Warmup: time.Second})
	run.Outcome = OutcomeIncomplete
	out := writeJSON(t, run)

	for name, want := range map[string]any{
		"schema_version":   1.0,
		"leettest_version": "v0.0.0-test",
		"target":           "localhost:50051",
		"outcome":          "incomplete",
		"started_at":       "2026-09-25T10:00:00Z",
		"duration_us":      3e6,
		"warmup_us":        1e6,
	} {
		if got := field(t, out, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestJSON_RateWithoutAMeasuredSpanIsNull(t *testing.T) {
	// No measured span: the engine leaves RPS at 0, which is not a rate.
	out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{Method: "/pkg.S/M"}}}))

	if v := field(t, field(t, out, "methods").([]any)[0], "rps"); v != nil {
		t.Errorf("rps = %v, want null when no span was measured", v)
	}
}

func TestJSON_UnannouncedStreamLimitIsNull(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{Connections: &engine.Connections{Open: 1}}))

	for _, name := range []string{"first_limit", "last_limit"} {
		if v := field(t, out, "connections", name); v != nil {
			t.Errorf("connections.%s = %v, want null: 0 is a valid limit", name, v)
		}
	}
}

func TestJSON_StartLagMaxWithoutCallsIsNull(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{}))

	if v := field(t, out, "start_lag", "max"); v != nil {
		t.Errorf("start_lag.max = %v, want null with no calls", v)
	}
}

// The seconds count every category under its contract name: JSON and
// Category.Name are one list. success is counted as "succeeded".
func TestJSON_SecondsCountEveryCategoryByItsName(t *testing.T) {
	keys := map[string]bool{}
	walk := func(rt reflect.Type) {
		for i := range rt.NumField() {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			keys[name] = true
		}
	}
	jr := reflect.TypeFor[JSONReport]()
	methods, ok := jr.FieldByName("Methods")
	if !ok {
		t.Fatal("JSONReport has no Methods")
	}
	seconds, ok := methods.Type.Elem().FieldByName("Seconds")
	if !ok {
		t.Fatal("a method has no Seconds")
	}
	walk(seconds.Type.Elem())

	for c := engine.CategoryUnknown; c <= engine.CategoryBadResponse; c++ {
		if c == engine.CategorySuccess {
			continue
		}
		if !keys[c.Name()] {
			t.Errorf("seconds have no %q key for category %d", c.Name(), c)
		}
	}
}

// Verdicts a script decides on are fields, not English: a rewording of a note
// must not break a pipeline.
func TestJSON_VerdictsAreFields(t *testing.T) {
	q := metrics.Quantile{Value: time.Millisecond, Exact: true, Defined: true}

	none := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{Method: "/pkg.S/M", Sent: 10}}}))
	if v := field(t, none, "invalid_reasons").([]any); len(v) != 0 {
		t.Errorf("invalid_reasons = %v, want [] for a valid run", v)
	}
	if v := field(t, field(t, none, "methods").([]any)[0], "invalid_reason"); v != nil {
		t.Errorf("invalid_reason = %v, want null for a method that measured load", v)
	}

	bad := writeJSON(t, jsonRun(engine.Report{
		CapHit: &engine.CapHit{At: time.Second, Unsent: 1}, RequestRejected: true,
		Methods: []engine.MethodReport{
			{Method: "/pkg.S/A", Sent: 5, Failed: 5, ClientError: 5},
			{Method: "/pkg.S/B", Sent: 5, Failed: 5, Rejected: engine.RefusalLatency{Count: 3, P50: q}, BadResponse: engine.RefusalLatency{Count: 2, P50: q}},
			{Method: "/pkg.S/C", Sent: 5, Failed: 5, Rejected: engine.RefusalLatency{Count: 5, P50: q}},
			{Method: "/pkg.S/D", Sent: 5, Failed: 5, BadResponse: engine.RefusalLatency{Count: 5, P50: q}},
		},
	}))
	reasons := field(t, bad, "invalid_reasons").([]any)
	if len(reasons) != 2 || reasons[0] != "in_flight_cap" || reasons[1] != "nothing_measured" {
		t.Errorf("invalid_reasons = %v, want [in_flight_cap nothing_measured]", reasons)
	}
	for i, want := range []string{"client_error", "mixed", "request_error", "bad_response"} {
		if got := field(t, field(t, bad, "methods").([]any)[i], "invalid_reason"); got != want {
			t.Errorf("methods[%d].invalid_reason = %v, want %q", i, got, want)
		}
	}

	limited := engine.Report{
		StreamWaited: 900, StreamCauseCalls: 900, StreamTailCalls: 900, StreamWaitP99: q,
		GeneratorCauseCalls: 7, GeneratorTailCalls: 3,
		Methods: []engine.MethodReport{{
			Method: "/pkg.S/M", Sent: 1000, P99: metrics.Quantile{Value: 50 * time.Millisecond, Exact: true, Defined: true},
			P99WithoutClientWaits: q,
		}},
	}
	out := writeJSON(t, jsonRun(limited))
	for name, want := range map[string]float64{"stream_calls": 900, "stream_tail_calls": 900, "generator_calls": 7, "generator_tail_calls": 3} {
		if v := field(t, out, "client_waits", name); v != want {
			t.Errorf("client_waits.%s = %v, want %v", name, v, want)
		}
	}
	if notes := field(t, out, "notes").([]any); len(notes) == 0 {
		t.Error("notes is empty: the text notes go along, marked unstable")
	}
}

// tail_wait_cause names the screen's verdict and nothing else: the cause ranked
// by the tail, null when the screen names none. Its value leads to its counts
// in client_waits. The old key limited_by is gone from every output.
func TestJSON_TailWaitCause(t *testing.T) {
	q := metrics.Quantile{Value: time.Millisecond, Exact: true, Defined: true}
	moved := []engine.MethodReport{{
		Method: "/pkg.S/M", Sent: 1000, P99: metrics.Quantile{Value: 50 * time.Millisecond, Exact: true, Defined: true},
		P99WithoutClientWaits: q,
	}}
	still := []engine.MethodReport{{Method: "/pkg.S/M", Sent: 1000, P99: q, P99WithoutClientWaits: q}}

	for _, tc := range []struct {
		name   string
		report engine.Report
		want   any
	}{
		{"generator", engine.Report{GeneratorCauseCalls: 5, GeneratorTailCalls: 5, Methods: moved}, "generator"},
		{"stream", engine.Report{StreamCauseCalls: 5, StreamTailCalls: 5, Methods: moved}, "stream"},
		{"connection", engine.Report{ConnectionCauseCalls: 5, ConnectionTailCalls: 5, Methods: moved}, "connection"},
		{"ranked by the tail, not the whole run", engine.Report{
			GeneratorCauseCalls: 900, GeneratorTailCalls: 1, StreamCauseCalls: 10, StreamTailCalls: 9, Methods: moved,
		}, "stream"},
		{"nothing moved", engine.Report{StreamCauseCalls: 5, StreamTailCalls: 5, Methods: still}, nil},
		{"causes in the run but none in the tail", engine.Report{GeneratorCauseCalls: 500, StreamCauseCalls: 40, Methods: moved}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if verdict := streamVerdict(tc.report) != ""; verdict != (tc.want != nil) {
				t.Fatalf("screen verdict = %v, want %v: the fixture does not test what it names", verdict, tc.want != nil)
			}

			var buf bytes.Buffer
			if err := WriteJSON(&buf, jsonRun(tc.report)); err != nil {
				t.Fatalf("WriteJSON: %v", err)
			}
			if bytes.Contains(buf.Bytes(), []byte(`"limited_by"`)) {
				t.Errorf("the output still has limited_by:\n%s", buf.String())
			}
			var out map[string]any
			if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
				t.Fatalf("output is not one JSON object: %v", err)
			}

			got := field(t, out, "tail_wait_cause")
			if got != tc.want {
				t.Fatalf("tail_wait_cause = %v, want %v", got, tc.want)
			}
			if s, ok := got.(string); ok {
				for _, key := range []string{s + "_calls", s + "_tail_calls"} {
					if _, ok := field(t, out, "client_waits").(map[string]any)[key]; !ok {
						t.Errorf("client_waits has no %s for tail_wait_cause %q", key, s)
					}
				}
			}
		})
	}
}

// A rate that is not a number would make encoding/json fail and leave stdout
// empty: it is null instead.
func TestJSON_ARateThatIsNotANumberIsNull(t *testing.T) {
	for _, rps := range []float64{math.NaN(), math.Inf(1)} {
		out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{Method: "/pkg.S/M", Sent: 1, RPS: rps}}}))
		if v := field(t, field(t, out, "methods").([]any)[0], "rps"); v != nil {
			t.Errorf("rps %v written as %v, want null", rps, v)
		}
	}
}

// A key that omitempty or a MarshalJSON drops would vanish from the output
// without the schema test seeing it: neither is allowed in the JSON types.
func TestJSON_NoKeyCanBeDroppedFromTheOutput(t *testing.T) {
	marshaler := reflect.TypeFor[json.Marshaler]()
	seen := map[reflect.Type]bool{}
	var check func(rt reflect.Type)
	check = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		if rt.Implements(marshaler) || reflect.PointerTo(rt).Implements(marshaler) {
			t.Errorf("%s has its own MarshalJSON", rt)
		}
		for i := range rt.NumField() {
			f := rt.Field(i)
			if strings.Contains(f.Tag.Get("json"), "omitempty") || strings.Contains(f.Tag.Get("json"), "omitzero") {
				t.Errorf("%s.%s may be dropped: %q", rt, f.Name, f.Tag.Get("json"))
			}
			check(f.Type)
		}
	}
	check(reflect.TypeFor[JSONReport]())
}

// The screen rounds for reading; JSON does not: two runs 0.1 ms apart at
// 12 ms stay apart, which comparing runs needs.
func TestJSON_LatenciesAreNotRoundedForTheScreen(t *testing.T) {
	out := writeJSON(t, jsonRun(engine.Report{Methods: []engine.MethodReport{{
		Method: "/pkg.S/M",
		P50:    metrics.Quantile{Value: 12_344 * time.Microsecond, Exact: true, Defined: true},
		P99:    metrics.Quantile{Value: 12_444 * time.Microsecond, Exact: true, Defined: true},
	}}}))

	lat := field(t, field(t, out, "methods").([]any)[0], "latency")
	if p50, p99 := field(t, lat, "p50", "us"), field(t, lat, "p99", "us"); p50 != 12344.0 || p99 != 12444.0 {
		t.Errorf("p50 %v, p99 %v; want 12344 and 12444, not the screen's 12.3ms", p50, p99)
	}
}
