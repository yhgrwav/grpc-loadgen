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
	"encoding/json"
	"errors"
	"io"
	"math"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/metrics"
)

// JSONSchemaVersion changes when a field is renamed, removed or changes type.
// Added fields and added enum values keep it. testdata/schema_v1.txt is the
// schema it names.
const JSONSchemaVersion = 1

// Outcomes of a run as JSON names them; each matches one exit code.
const (
	OutcomeComplete   = "complete"
	OutcomeInvalid    = "invalid"
	OutcomeIncomplete = "incomplete"
)

// JSONRun is what the JSON report needs beyond the engine's report.
type JSONRun struct {
	Target    string
	Version   string
	Outcome   string
	StartedAt time.Time
	Run       RunReport
}

// JSONReport is the report as --output json writes it. A pointer is the only
// thing that may be null: a value the run did not produce, never a zero that
// could pass for a measurement. Durations are whole microseconds.
type JSONReport struct {
	SchemaVersion   int    `json:"schema_version"`
	LeetTestVersion string `json:"leettest_version"`
	Target          string `json:"target"`
	Outcome         string `json:"outcome"`
	// InvalidReasons are why the run is invalid: in_flight_cap, nothing_measured,
	// clock_step.
	InvalidReasons []string `json:"invalid_reasons"`
	// TailWaitCause is the client-side wait that set the tail: generator, stream
	// or connection; null without that verdict.
	TailWaitCause *string `json:"tail_wait_cause"`
	// ClockStepNS is the host clock step: every latency and wait is ± it.
	ClockStepNS       int64           `json:"clock_step_ns"`
	StartedAt         string          `json:"started_at"`
	DurationUS        int64           `json:"duration_us"`
	PlannedUS         int64           `json:"planned_us"`
	WarmupUS          int64           `json:"warmup_us"`
	Sent              int             `json:"sent"`
	Failed            int             `json:"failed"`
	Aborted           int             `json:"aborted"`
	NotSent           int             `json:"not_sent"`
	NotSentGenerator  int             `json:"not_sent_generator"`
	NotSentStream     int             `json:"not_sent_stream"`
	NotSentConnection int             `json:"not_sent_connection"`
	WarmupSent        int             `json:"warmup_sent"`
	WarmupFailed      int             `json:"warmup_failed"`
	WarmupNotSent     int             `json:"warmup_not_sent"`
	CapHit            *jsonCapHit     `json:"cap_hit"`
	StartLag          jsonStartLag    `json:"start_lag"`
	Connections       *jsonConns      `json:"connections"`
	ClientWaits       jsonClientWaits `json:"client_waits"`
	Methods           []jsonMethod    `json:"methods"`
	Unchecked         []jsonUnchecked `json:"unchecked"`
	// Notes are the text report's notes: for people, and reworded freely.
	Notes []string `json:"notes"`
}

type jsonQuantile struct {
	US         int64 `json:"us"`
	LowerBound bool  `json:"lower_bound"`
}

type jsonCapHit struct {
	AtUS         int64 `json:"at_us"`
	Unsent       int   `json:"unsent"`
	OverDeadline int   `json:"over_deadline"`
}

type jsonStartLag struct {
	P99 *jsonQuantile `json:"p99"`
	Max *jsonQuantile `json:"max"`
}

type jsonConns struct {
	Open         int  `json:"open"`
	Reconnects   int  `json:"reconnects"`
	FirstLimit   *int `json:"first_limit"`
	LastLimit    *int `json:"last_limit"`
	LimitChanges int  `json:"limit_changes"`
}

type jsonLatency struct {
	Min *jsonQuantile `json:"min"`
	P50 *jsonQuantile `json:"p50"`
	P90 *jsonQuantile `json:"p90"`
	P95 *jsonQuantile `json:"p95"`
	P99 *jsonQuantile `json:"p99"`
	Max *jsonQuantile `json:"max"`
}

type jsonAnswers struct {
	Count int           `json:"count"`
	P50   *jsonQuantile `json:"p50"`
	P90   *jsonQuantile `json:"p90"`
	P95   *jsonQuantile `json:"p95"`
	P99   *jsonQuantile `json:"p99"`
	Max   *jsonQuantile `json:"max"`
}

type jsonCode struct {
	Code       string `json:"code"`
	Count      int    `json:"count"`
	FromTarget bool   `json:"from_target"`
}

type jsonMethod struct {
	Method string `json:"method"`
	// InvalidReason says the method measured nothing about load: request_error,
	// client_error, bad_response or mixed; null when it did.
	InvalidReason         *string       `json:"invalid_reason"`
	Sent                  int           `json:"sent"`
	Failed                int           `json:"failed"`
	Aborted               int           `json:"aborted"`
	NotSent               int           `json:"not_sent"`
	NotSentGenerator      int           `json:"not_sent_generator"`
	NotSentStream         int           `json:"not_sent_stream"`
	NotSentConnection     int           `json:"not_sent_connection"`
	WarmupSent            int           `json:"warmup_sent"`
	WarmupFailed          int           `json:"warmup_failed"`
	WarmupNotSent         int           `json:"warmup_not_sent"`
	RPS                   *float64      `json:"rps"`
	TimeoutUS             int64         `json:"timeout_us"`
	PlannedRPSLow         int           `json:"planned_rps_low"`
	PlannedRPSHigh        int           `json:"planned_rps_high"`
	Latency               jsonLatency   `json:"latency"`
	P99WithoutClientWaits *jsonQuantile `json:"p99_without_client_waits"`
	Observations          int           `json:"observations"`
	Censored              int           `json:"censored"`
	InvalidLatencies      int           `json:"invalid_latencies"`
	TimedOut              int           `json:"timed_out"`
	TimedOutAfterWait     int           `json:"timed_out_after_wait"`
	CutOff                int           `json:"cut_off"`
	Unreachable           int           `json:"unreachable"`
	Unclassified          int           `json:"unclassified"`
	OutsideTimeline       int           `json:"outside_timeline"`
	RequestError          jsonAnswers   `json:"request_error"`
	Overload              jsonAnswers   `json:"overload"`
	Failure               jsonAnswers   `json:"failure"`
	BadResponse           jsonAnswers   `json:"bad_response"`
	ClientError           int           `json:"client_error"`
	FailureCodes          []jsonCode    `json:"failure_codes"`
	SilentFromS           *int          `json:"silent_from_s"`
	LastAnswerAtUS        *int64        `json:"last_answer_at_us"`
	Seconds               []jsonSecond  `json:"seconds"`
}

// jsonSecond counts each category under its Category.Name, but for success.
type jsonSecond struct {
	Warmup             bool  `json:"warmup"`
	Begun              int   `json:"begun"`
	Succeeded          int   `json:"succeeded"`
	Overload           int   `json:"overload"`
	Failure            int   `json:"failure"`
	ClientError        int   `json:"client_error"`
	BadResponse        int   `json:"bad_response"`
	RequestError       int   `json:"request_error"`
	TimedOut           int   `json:"timed_out"`
	NotSentGenerator   int   `json:"not_sent_generator"`
	NotSentStream      int   `json:"not_sent_stream"`
	NotSentConnection  int   `json:"not_sent_connection"`
	Unreachable        int   `json:"unreachable"`
	CutOff             int   `json:"cut_off"`
	Aborted            int   `json:"aborted"`
	Unclassified       int   `json:"unclassified"`
	InFlight           int   `json:"in_flight"`
	LagCalls           int   `json:"lag_calls"`
	LagSumUS           int64 `json:"lag_sum_us"`
	LagMaxUS           int64 `json:"lag_max_us"`
	ObservedCalls      int   `json:"observed_calls"`
	ObservedLagSumUS   int64 `json:"observed_lag_sum_us"`
	TransportWaitSumUS int64 `json:"transport_wait_sum_us"`
	ServiceTimeSumUS   int64 `json:"service_time_sum_us"`
}

type jsonUnchecked struct {
	Method string `json:"method"`
	// Reason is for scripts: reflection_off or reflection_failed. Error is
	// for people and changes with grpc-go.
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

// canonicalCodes are the gRPC codes as grpc/grpc doc/statuscodes.md names
// them, keyed by grpc-go's String.
var canonicalCodes = func() map[string]string {
	names := []string{
		"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND",
		"ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION",
		"ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS",
		"UNAUTHENTICATED",
	}
	m := make(map[string]string, len(names))
	for i, name := range names {
		m[codes.Code(i).String()] = name
	}

	return m
}()

func micros(d time.Duration) int64 { return d.Microseconds() }

func quantile(q metrics.Quantile) *jsonQuantile {
	if !q.Defined {
		return nil
	}

	return &jsonQuantile{US: micros(q.Value), LowerBound: !q.Exact}
}

func answers(r engine.RefusalLatency) jsonAnswers {
	return jsonAnswers{
		Count: r.Count, P50: quantile(r.P50), P90: quantile(r.P90), P95: quantile(r.P95),
		P99: quantile(r.P99), Max: quantile(r.Max),
	}
}

// NewJSONReport builds the JSON report of a finished run.
func NewJSONReport(run JSONRun) JSONReport {
	r := &run.Run.Report
	out := JSONReport{
		SchemaVersion:     JSONSchemaVersion,
		LeetTestVersion:   run.Version,
		Target:            run.Target,
		Outcome:           run.Outcome,
		StartedAt:         run.StartedAt.UTC().Format(time.RFC3339Nano),
		DurationUS:        micros(r.Duration),
		PlannedUS:         micros(r.Planned),
		WarmupUS:          micros(r.Warmup),
		Sent:              r.Sent,
		Failed:            r.Failed,
		Aborted:           r.Aborted,
		NotSent:           r.NotSent,
		NotSentGenerator:  r.NotSentGenerator,
		NotSentStream:     r.NotSentStream,
		NotSentConnection: r.NotSentConnection,
		WarmupSent:        r.WarmupSent,
		WarmupFailed:      r.WarmupFailed,
		WarmupNotSent:     r.WarmupNotSent,
		StartLag:          jsonStartLag{P99: quantile(r.StartLagP99)},
		InvalidReasons:    make([]string, 0, 2),
		TailWaitCause:     tailWaitCause(*r),
		ClockStepNS:       int64(run.Run.ClockStep),
		ClientWaits: jsonClientWaits{
			GeneratorCalls: r.GeneratorCauseCalls, StreamCalls: r.StreamCauseCalls, ConnectionCalls: r.ConnectionCauseCalls,
			GeneratorTailCalls: r.GeneratorTailCalls, StreamTailCalls: r.StreamTailCalls, ConnectionTailCalls: r.ConnectionTailCalls,
		},
		Notes:     runNotes(run.Run),
		Methods:   make([]jsonMethod, 0, len(r.Methods)),
		Unchecked: make([]jsonUnchecked, 0, len(run.Run.Unchecked)),
	}
	// The maximum is exact, but there is none without a call.
	if r.StartLagP99.Defined {
		out.StartLag.Max = &jsonQuantile{US: micros(r.StartLagMax)}
	}
	if c := r.CapHit; c != nil {
		out.CapHit = &jsonCapHit{AtUS: micros(c.At), Unsent: c.Unsent, OverDeadline: c.OverDeadline}
	}
	if c := r.Connections; c != nil {
		conns := &jsonConns{Open: c.Open, Reconnects: c.Reconnects, LimitChanges: c.LimitChanges}
		// 0 is a limit a target may announce: unannounced is null, not 0.
		if c.LimitAnnounced || c.LimitChanges > 0 {
			first, last := int(c.FirstLimit), int(c.LastLimit)
			conns.FirstLimit = &first
			if c.LimitAnnounced {
				conns.LastLimit = &last
			}
		}
		out.Connections = conns
	}
	for i := range r.Methods {
		out.Methods = append(out.Methods, jsonMethodOf(&r.Methods[i], r.Warmup))
	}
	if note := uncheckedNote(run.Run.Unchecked); note != "" {
		out.Notes = append(out.Notes, note)
	}
	if r.CapHit != nil {
		out.InvalidReasons = append(out.InvalidReasons, "in_flight_cap")
	}
	for i := range out.Methods {
		if out.Methods[i].InvalidReason != nil {
			out.InvalidReasons = append(out.InvalidReasons, "nothing_measured")

			break
		}
	}
	if ClockTooCoarse(run.Run) {
		out.InvalidReasons = append(out.InvalidReasons, "clock_step")
	}
	for _, u := range run.Run.Unchecked {
		reason := "reflection_failed"
		if errors.Is(u.Err, descriptor.ErrReflectionUnsupported) {
			reason = "reflection_off"
		}
		msg := ""
		if u.Err != nil {
			msg = u.Err.Error()
		}
		out.Unchecked = append(out.Unchecked, jsonUnchecked{Method: u.Method, Reason: reason, Error: msg})
	}

	return out
}

func jsonMethodOf(m *engine.MethodReport, warmup time.Duration) jsonMethod {
	out := jsonMethod{
		Method: m.Method, InvalidReason: invalidReason(m), Sent: m.Sent, Failed: m.Failed, Aborted: m.Aborted,
		NotSent: m.NotSent, NotSentGenerator: m.NotSentGenerator, NotSentStream: m.NotSentStream,
		NotSentConnection: m.NotSentConnection,
		WarmupSent:        m.WarmupSent, WarmupFailed: m.WarmupFailed, WarmupNotSent: m.WarmupNotSent,
		TimeoutUS: micros(m.Timeout), PlannedRPSLow: m.RPSLow, PlannedRPSHigh: m.RPSHigh,
		Latency: jsonLatency{
			Min: quantile(m.Min), P50: quantile(m.P50), P90: quantile(m.P90),
			P95: quantile(m.P95), P99: quantile(m.P99), Max: quantile(m.Max),
		},
		P99WithoutClientWaits: quantile(m.P99WithoutClientWaits),
		Observations:          m.Latencies, Censored: m.Censored, InvalidLatencies: m.Invalid,
		TimedOut: m.TimedOut, TimedOutAfterWait: m.TimedOutAfterWait,
		CutOff: m.CutOff, Unreachable: m.Unanswered, Unclassified: m.Unclassified,
		OutsideTimeline: m.OutsideTimeline,
		RequestError:    answers(m.Rejected), Overload: answers(m.Overload), Failure: answers(m.Failure),
		BadResponse: answers(m.BadResponse), ClientError: m.ClientError,
		FailureCodes: make([]jsonCode, 0, len(m.FailureCodes)),
		SilentFromS:  m.SilentFrom,
		Seconds:      make([]jsonSecond, 0, len(m.Seconds)),
	}
	// No call, no rate: a zero would read as a target that took none. NaN or
	// Inf would fail the whole encoding and leave stdout empty.
	if m.Sent > 0 && !math.IsNaN(m.RPS) && !math.IsInf(m.RPS, 0) {
		rps := m.RPS
		out.RPS = &rps
	}
	if m.LastAnswerAt != nil {
		at := micros(*m.LastAnswerAt)
		out.LastAnswerAtUS = &at
	}
	for _, c := range m.FailureCodes {
		name, ok := canonicalCodes[c.Code]
		if !ok {
			name = c.Code
		}
		out.FailureCodes = append(out.FailureCodes, jsonCode{Code: name, Count: c.Count, FromTarget: c.FromTarget})
	}
	for i := range m.Seconds {
		s := &m.Seconds[i]
		out.Seconds = append(out.Seconds, jsonSecond{
			Warmup: time.Duration(i)*time.Second < warmup,
			Begun:  s.Begun, Succeeded: s.Succeeded, Overload: s.Overload, Failure: s.Failure,
			ClientError: s.ClientError, BadResponse: s.BadResponse, RequestError: s.RequestFailed,
			TimedOut: s.TimedOut, NotSentGenerator: s.NotSentGenerator, NotSentStream: s.NotSentStream,
			NotSentConnection: s.NotSentConnection, Unreachable: s.Unanswered, CutOff: s.CutOff,
			Aborted: s.Aborted, Unclassified: s.Unclassified, InFlight: s.InFlight,
			LagCalls: s.LagCalls, LagSumUS: micros(s.LagSum), LagMaxUS: micros(s.LagMax),
			ObservedCalls: s.ObservedCalls, ObservedLagSumUS: micros(s.ObservedLagSum),
			TransportWaitSumUS: micros(s.TransportWaitSum), ServiceTimeSumUS: micros(s.ServiceTimeSum),
		})
	}

	return out
}

// WriteJSON writes the report as one JSON object and a newline.
func WriteJSON(w io.Writer, run JSONRun) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	return enc.Encode(NewJSONReport(run))
}

// jsonClientWaits count the calls that waited over the floor for each cause,
// in all and among those that set each method's p99: the numbers tail_wait_cause
// ranks.
type jsonClientWaits struct {
	GeneratorCalls      int `json:"generator_calls"`
	StreamCalls         int `json:"stream_calls"`
	ConnectionCalls     int `json:"connection_calls"`
	GeneratorTailCalls  int `json:"generator_tail_calls"`
	StreamTailCalls     int `json:"stream_tail_calls"`
	ConnectionTailCalls int `json:"connection_tail_calls"`
}

// tailWaitCause names verdictCause the way JSON does.
func tailWaitCause(report engine.Report) *string {
	c, ok := verdictCause(report)
	if !ok {
		return nil
	}
	name := map[string]string{causeGenerator: "generator", causeStream: "stream", causeConnection: "connection"}[c.what]

	return &name
}

// invalidReason is why a method measured nothing about load, by the rule of
// invalidNote, or nil when it did.
func invalidReason(m *engine.MethodReport) *string {
	if invalidNote(m, "") == "" {
		return nil
	}
	reason := "mixed"
	switch m.Sent {
	case m.Rejected.Count:
		reason = "request_error"
	case m.ClientError:
		reason = "client_error"
	case m.BadResponse.Count:
		reason = "bad_response"
	}

	return &reason
}
