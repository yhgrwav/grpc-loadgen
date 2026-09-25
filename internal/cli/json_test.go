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
