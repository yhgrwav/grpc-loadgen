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

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	channelzpb "google.golang.org/grpc/channelz/grpc_channelz_v1"
	"google.golang.org/grpc/health/grpc_health_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/stats"
	"google.golang.org/protobuf/proto"

	"github.com/yhgrwav/leettest/internal/cli"
	"github.com/yhgrwav/leettest/pkg/config"
	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
)

const (
	checkMethod   = "grpc.health.v1.Health/Check"
	missingMethod = "grpc.health.v1.Health/DoesNotExist"
)

// health answers every Check and counts what reached it.
type health struct {
	grpc_health_v1.UnimplementedHealthServer

	calls atomic.Int64
	delay time.Duration

	// service is the field of the last request, to compare with the config.
	mu      sync.Mutex
	service string
}

func (h *health) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	h.calls.Add(1)

	h.mu.Lock()
	h.service = req.GetService()
	h.mu.Unlock()

	if h.delay > 0 {
		select {
		case <-time.After(h.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// connEnds signals when a client connection to the server is gone.
type connEnds struct {
	once sync.Once
	done chan struct{}
}

func (c *connEnds) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (c *connEnds) HandleRPC(context.Context, stats.RPCStats)                         {}
func (c *connEnds) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (c *connEnds) HandleConn(_ context.Context, s stats.ConnStats) {
	if _, ok := s.(*stats.ConnEnd); ok {
		c.once.Do(func() { close(c.done) })
	}
}

// recorder keeps the last request of the interop test service and of channelz:
// between them they carry a nested message, bytes, an enum, a JSON-named field
// and an int64, with no code generated here.
type recorder struct {
	testgrpc.UnimplementedTestServiceServer
	channelzpb.UnimplementedChannelzServer

	mu     sync.Mutex
	simple *testgrpc.SimpleRequest
	socket *channelzpb.GetSocketRequest
}

func (r *recorder) UnaryCall(_ context.Context, req *testgrpc.SimpleRequest) (*testgrpc.SimpleResponse, error) {
	r.mu.Lock()
	r.simple = proto.Clone(req).(*testgrpc.SimpleRequest)
	r.mu.Unlock()

	return &testgrpc.SimpleResponse{}, nil
}

func (r *recorder) GetSocket(_ context.Context, req *channelzpb.GetSocketRequest) (*channelzpb.GetSocketResponse, error) {
	r.mu.Lock()
	r.socket = proto.Clone(req).(*channelzpb.GetSocketRequest)
	r.mu.Unlock()

	return &channelzpb.GetSocketResponse{}, nil
}

func (r *recorder) lastSimple() *testgrpc.SimpleRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.simple
}

func (r *recorder) lastSocket() *channelzpb.GetSocketRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.socket
}

type liveTarget struct {
	addr     string
	health   *health
	recorder *recorder
	ended    <-chan struct{}
}

// startTarget runs a plaintext health service on a real TCP port, because the
// CLI dials an address from the config and cannot be handed a dialer.
func startTarget(t *testing.T) liveTarget {
	t.Helper()

	return startServer(t, 0, false)
}

// startSlowTarget is startTarget with every answer held back by delay.
func startSlowTarget(t *testing.T, delay time.Duration) liveTarget {
	t.Helper()

	return startServer(t, delay, false)
}

// startReflectingTarget is startTarget with server reflection on, so the CLI
// can learn the request schema from it.
func startReflectingTarget(t *testing.T) liveTarget {
	t.Helper()

	return startServer(t, 0, true)
}

func startServer(t *testing.T, delay time.Duration, withReflection bool) liveTarget {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	h := &health{delay: delay}
	ends := &connEnds{done: make(chan struct{})}
	srv := grpc.NewServer(grpc.StatsHandler(ends))
	grpc_health_v1.RegisterHealthServer(srv, h)

	rec := &recorder{}
	testgrpc.RegisterTestServiceServer(srv, rec)
	channelzpb.RegisterChannelzServer(srv, rec)

	if withReflection {
		reflection.Register(srv)
	}

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return liveTarget{addr: lis.Addr().String(), health: h, recorder: rec, ended: ends.done}
}

// silentTarget accepts TCP and never speaks.
func silentTarget(t *testing.T) string {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	accepted := make(chan net.Conn, 16)
	go func() {
		for {
			conn, acceptErr := lis.Accept()
			if acceptErr != nil {
				return
			}
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = lis.Close()
		for {
			select {
			case conn := <-accepted:
				_ = conn.Close()
			default:
				return
			}
		}
	})

	return lis.Addr().String()
}

func closedPort(t *testing.T) string {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()

	return addr
}

// tlsLine is the app.tls line of the config; empty leaves TLS at its default.
func writeConfig(t *testing.T, addr, method, tlsLine string) string {
	t.Helper()

	return writeConfigWith(t, addr, method, tlsLine, "")
}

// writeConfigWith adds callLines, indented as fields of the call, to the config.
func writeConfigWith(t *testing.T, addr, method, tlsLine, callLines string) string {
	t.Helper()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}

	cfg := fmt.Sprintf(`app:
  target:
    ip: %s
    port: %s
%s
load:
  calls:
    - method: %s
      rps: 50
      duration: 300ms
%s`, host, port, tlsLine, method, callLines)

	path := filepath.Join(t.TempDir(), "leettest.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

const plaintext = "  tls: false"

type result struct {
	stdout, stderr string
	err            error
}

// runCLI runs the command with its own writers and settings directory, and
// fails the test if it has not returned by limit.
func runCLI(ctx context.Context, t *testing.T, limit time.Duration, args ...string) result {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, nil, args, &stdout, &stderr) }()

	select {
	case err := <-done:
		return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
	case <-time.After(limit):
		t.Fatalf("run has not returned after %v", limit)

		return result{}
	}
}

// reportRow reads the sent and failed columns of a method's row in the report.
func reportRow(t *testing.T, report, method string) (sent, failed int) {
	t.Helper()

	for line := range strings.Lines(report) {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != method {
			continue
		}

		sent, errSent := strconv.Atoi(fields[1])
		failed, errFailed := strconv.Atoi(fields[2])
		if errSent != nil || errFailed != nil {
			t.Fatalf("row %q: sent/failed are not numbers", line)
		}

		return sent, failed
	}

	t.Fatalf("report has no row for %s:\n%s", method, report)

	return 0, 0
}

// --- the real target ----------------------------------------------------

func TestRun_EverySentRequestReachesTheTarget(t *testing.T) {
	target := startTarget(t)

	res := runCLI(t.Context(), t, 10*time.Second, "-c", writeConfig(t, target.addr, checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}

	sent, failed := reportRow(t, res.stdout, checkMethod)
	if sent == 0 {
		t.Fatal("report shows nothing sent")
	}
	if got := target.health.calls.Load(); int64(sent) != got {
		t.Errorf("report says %d sent, target received %d", sent, got)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0 against a target that answers everything", failed)
	}
}

func TestRun_MissingMethodIsReportedNotFatal(t *testing.T) {
	target := startTarget(t)

	// Every call rejected: the run finishes and reports, and is invalid.
	res := runCLI(t.Context(), t, 10*time.Second, "-c", writeConfig(t, target.addr, missingMethod, plaintext))
	if !errors.Is(res.err, ErrInvalidRun) {
		t.Fatalf("run: %v, want the run to finish, report the failures and be invalid", res.err)
	}

	sent, failed := reportRow(t, res.stdout, missingMethod)
	if sent == 0 || failed != sent {
		t.Errorf("sent %d, failed %d, want every sent request failed", sent, failed)
	}
}

func TestRun_ClosesTheConnectionAfterTheRun(t *testing.T) {
	target := startTarget(t)

	res := runCLI(t.Context(), t, 10*time.Second, "-c", writeConfig(t, target.addr, checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}

	select {
	case <-target.ended:
	case <-time.After(2 * time.Second):
		t.Error("the connection is still open 2s after run returned")
	}
}

// --- connecting ---------------------------------------------------------

func TestRun_UnreachableTargetFailsWithoutAReport(t *testing.T) {
	addr := closedPort(t)

	res := runCLI(t.Context(), t, 3*time.Second, "-c", writeConfig(t, addr, checkMethod, plaintext))
	if res.err == nil {
		t.Fatal("run against a closed port succeeded")
	}
	if !strings.Contains(res.err.Error(), addr) {
		t.Errorf("error %q does not name the address %s", res.err, addr)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want no report: there was no run", res.stdout)
	}
}

func TestRun_SaysWhatItIsConnectingTo(t *testing.T) {
	addr := silentTarget(t)

	res := runCLI(t.Context(), t, 3*time.Second,
		"-connect-timeout", "200ms", "-c", writeConfig(t, addr, checkMethod, plaintext))
	if !strings.Contains(res.stderr, "connecting to "+addr) {
		t.Errorf("stderr = %q, want a line saying what it is waiting for", res.stderr)
	}
}

func TestRun_ConnectTimeoutBoundsASilentTarget(t *testing.T) {
	addr := silentTarget(t)

	res := runCLI(t.Context(), t, 3*time.Second,
		"-connect-timeout", "200ms", "-c", writeConfig(t, addr, checkMethod, plaintext))
	if res.err == nil {
		t.Fatal("run against a silent target succeeded")
	}
	if !strings.Contains(res.err.Error(), addr) {
		t.Errorf("error %q does not name the address %s", res.err, addr)
	}
}

func TestRun_CancelDuringConnectPrintsNoReport(t *testing.T) {
	addr := silentTarget(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res := runCLI(ctx, t, 3*time.Second, "-c", writeConfig(t, addr, checkMethod, plaintext))
	if res.err == nil {
		t.Error("cancelled connect returned no error, want a non-zero exit")
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want no report: there was no run", res.stdout)
	}
}

func TestRun_DefaultTLSFailureSaysHowToTurnItOff(t *testing.T) {
	target := startTarget(t)

	res := runCLI(t.Context(), t, 3*time.Second, "-c", writeConfig(t, target.addr, checkMethod, ""))
	if res.err == nil {
		t.Fatal("TLS connect to a plaintext server succeeded")
	}
	if !strings.Contains(res.err.Error(), "app.tls") {
		t.Errorf("error %q does not say that TLS came from the default and how to change it", res.err)
	}
}

// --- the fake target ----------------------------------------------------

func TestRun_FakeDoesNotConnect(t *testing.T) {
	res := runCLI(t.Context(), t, 10*time.Second,
		"-fake", "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
	if res.err != nil {
		t.Errorf("run: %v, want -fake to work without a reachable target", res.err)
	}
}

func TestRun_FakeSendsNothingToTheTarget(t *testing.T) {
	target := startTarget(t)

	res := runCLI(t.Context(), t, 10*time.Second,
		"-fake", "-c", writeConfig(t, target.addr, checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if n := target.health.calls.Load(); n != 0 {
		t.Errorf("target received %d calls under -fake, want none", n)
	}
}

func TestRun_FakeIsNamedInTheReport(t *testing.T) {
	res := runCLI(t.Context(), t, 10*time.Second,
		"-fake", "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}

	header, _, _ := strings.Cut(res.stdout, "\n")
	if !strings.Contains(strings.ToLower(header), "fake") {
		t.Errorf("report header %q does not say the numbers come from the fake target", header)
	}
}

func TestRun_FakeTuningWithoutFakeIsAnError(t *testing.T) {
	for _, flag := range []string{"-fake-delay=5ms", "-fake-jitter=5ms", "-fake-fail-ratio=0.1"} {
		t.Run(flag, func(t *testing.T) {
			res := runCLI(t.Context(), t, 3*time.Second,
				flag, "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
			if res.err == nil || !strings.Contains(res.err.Error(), "-fake") {
				t.Errorf("err = %v, want an error pointing at -fake", res.err)
			}
		})
	}
}

// --- timeout and the in-flight budget -----------------------------------

func TestRun_TimeoutFromTheConfigCensorsAHungTarget(t *testing.T) {
	target := startSlowTarget(t, 5*time.Second)
	cfg := writeConfigWith(t, target.addr, checkMethod, plaintext, "      timeout: 100ms\n")

	res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg)
	if res.err != nil {
		t.Fatalf("run: %v, want timeouts to be measurements, not a failed run", res.err)
	}

	sent, failed := reportRow(t, res.stdout, checkMethod)
	if sent == 0 || failed != sent {
		t.Errorf("sent %d, failed %d, want every request timed out", sent, failed)
	}
	if !strings.Contains(res.stdout, "abandoned before answering") {
		t.Errorf("report does not mark the percentiles as lower bounds:\n%s", res.stdout)
	}
}

func TestRun_OverBudgetConfigFailsBeforeConnecting(t *testing.T) {
	// 50 RPS x the default 2s = 100 in flight against a hung target, over a
	// cap of 10. A silent target would hold a connect attempt for 10s, so a
	// quick return proves the check came first.
	addr := silentTarget(t)

	res := runCLI(t.Context(), t, 3*time.Second,
		"-max-in-flight", "10", "-c", writeConfig(t, addr, checkMethod, plaintext))
	if res.err == nil {
		t.Fatal("run succeeded, want the in-flight budget to reject the config")
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want no report", res.stdout)
	}
}

func TestRun_OverBudgetErrorGivesBothWaysOut(t *testing.T) {
	res := runCLI(t.Context(), t, 3*time.Second,
		"-max-in-flight", "10", "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
	if res.err == nil {
		t.Fatal("run succeeded, want the in-flight budget to reject the config")
	}

	// Cap 10 at 50 RPS: a slot for the call on the window's edge and 5 for
	// 100ms of late release leave 4, a timeout of 80ms (81ms rounds up to 5);
	// keeping 2s needs 100 + 1 + 5 = 106. The error says why the numbers are
	// not 200ms and 100.
	for _, want := range []string{"timeout", "80ms", "-max-in-flight", "106", "100ms"} {
		if !strings.Contains(res.err.Error(), want) {
			t.Errorf("error %q lacks %q", res.err, want)
		}
	}
}

// The margin is explained once, and that once names both of its parts: said
// twice it reads as two reserves, and the engine's line alone left the edge
// slot out of a number that holds it.
// 1000 rps with 2s against a cap of 2000: 2000 + 100 + 1 = 2101 needed, and
// 1899ms is the largest that fits: 1899 + 100 + 1 = 2000.
func TestBudgetAdvice_FollowsTheCall(t *testing.T) {
	opts := engine.Options{
		Calls: []engine.Call{{Method: "a", Timeout: 2 * time.Second,
			Stages: []engine.Stage{{StartRPS: 1000, TargetRPS: 1000, Duration: time.Second}}}},
		Sender:      engine.FakeSender{},
		MaxInFlight: 2000,
	}
	err := engine.CheckOptions(opts)
	if err == nil {
		t.Fatal("err = nil, want the budget refused")
	}

	advice := withBudgetAdvice(err, opts).Error()
	for _, want := range []string{"up to 2101 requests", "at most 1.899s ", "-max-in-flight 2101"} {
		if !strings.Contains(advice, want) {
			t.Errorf("%q lacks %q", advice, want)
		}
	}
}

func TestRun_OverBudgetErrorExplainsTheMarginOnce(t *testing.T) {
	for _, c := range []struct{ name, cap string }{
		{"a timeout fits", "10"},
		{"no timeout fits", "3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := runCLI(t.Context(), t, 3*time.Second,
				"-max-in-flight", c.cap, "-c", writeConfig(t, closedPort(t), checkMethod, plaintext))
			if res.err == nil {
				t.Fatal("run succeeded, want the in-flight budget to reject the config")
			}

			text := strings.Join(strings.Fields(res.err.Error()), " ")
			if n := strings.Count(text, "past their deadline"); n != 1 {
				t.Errorf("the late release is explained %d times, want once:\n%s", n, res.err)
			}
			if !strings.Contains(text, "window's edge") {
				t.Errorf("the margin leaves out the slot for the call on the window's edge:\n%s", res.err)
			}
		})
	}
}

func TestExampleConfigFitsTheDefaultInFlightCap(t *testing.T) {
	cfg, err := config.LoadFile(filepath.Join("..", "..", "examples", "leettest.yaml"))
	if err != nil {
		t.Fatalf("load example: %v", err)
	}

	_, err = engine.New(engine.Options{
		Calls:       cli.CallsFromConfig(cfg),
		Sender:      engine.FakeSender{},
		MaxInFlight: defaultMaxInFlight,
	})
	if err != nil {
		t.Errorf("the shipped example is rejected with the default cap: %v", err)
	}
}

// --- request data -------------------------------------------------------

// dataConfig is a config whose calls are given in full, data included.
func dataConfig(t *testing.T, addr, calls string) string {
	t.Helper()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}

	cfg := fmt.Sprintf("app:\n  target:\n    ip: %s\n    port: %s\n  tls: false\nload:\n  calls:\n%s", host, port, calls)

	path := filepath.Join(t.TempDir(), "leettest.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func checkCall(data string) string {
	return "    - method: " + checkMethod + "\n      rps: 50\n      duration: 300ms\n" + data
}

func TestRun_DataReachesTheTarget(t *testing.T) {
	target := startReflectingTarget(t)
	cfg := dataConfig(t, target.addr, checkCall("      data:\n        service: wallet\n"))

	res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg)
	if res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}

	target.health.mu.Lock()
	got := target.health.service
	target.health.mu.Unlock()

	if got != "wallet" {
		t.Errorf("target received service = %q, want the %q from data", got, "wallet")
	}
}

func TestRun_EmptyDataIsAValidBody(t *testing.T) {
	target := startReflectingTarget(t)
	cfg := dataConfig(t, target.addr, checkCall("      data: {}\n"))

	if res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg); res.err != nil {
		t.Errorf("run with empty data: %v", res.err)
	}
}

func TestRun_UnknownDataFieldFailsBeforeTheRun(t *testing.T) {
	target := startReflectingTarget(t)
	cfg := dataConfig(t, target.addr, checkCall("      data:\n        no_such_field: 1\n"))

	res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg)
	// The YAML parser quotes the config in its errors, so a message check
	// alone would pass on a config that was never read. The type says which
	// step failed.
	if !errors.Is(res.err, cli.ErrRequestData) {
		t.Fatalf("err = %v, want cli.ErrRequestData", res.err)
	}
	for _, want := range []string{checkMethod, "no_such_field"} {
		if !strings.Contains(res.err.Error(), want) {
			t.Errorf("error %q does not name %q", res.err, want)
		}
	}
	if n := target.health.calls.Load(); n != 0 {
		t.Errorf("target received %d calls, want none before a config error", n)
	}
}

func TestRun_WrongDataTypeFailsBeforeTheRun(t *testing.T) {
	target := startReflectingTarget(t)
	cfg := dataConfig(t, target.addr, checkCall("      data:\n        service: 5\n"))

	res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg)
	if !errors.Is(res.err, cli.ErrRequestData) || !strings.Contains(res.err.Error(), "service") {
		t.Errorf("err = %v, want cli.ErrRequestData naming the field service", res.err)
	}
	if n := target.health.calls.Load(); n != 0 {
		t.Errorf("target received %d calls, want none before a config error", n)
	}
}

func TestRun_DataWithoutReflectionSaysSo(t *testing.T) {
	target := startTarget(t)
	cfg := dataConfig(t, target.addr, checkCall("      data:\n        service: wallet\n"))

	res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg)
	if !errors.Is(res.err, descriptor.ErrReflectionUnsupported) {
		t.Fatalf("err = %v, want descriptor.ErrReflectionUnsupported", res.err)
	}
	for _, want := range []string{checkMethod, "reflection"} {
		if !strings.Contains(res.err.Error(), want) {
			t.Errorf("error %q does not name %q", res.err, want)
		}
	}
	if n := target.health.calls.Load(); n != 0 {
		t.Errorf("target received %d calls, want none before a config error", n)
	}
}

func TestRun_DataForAMissingMethodFailsBeforeTheRun(t *testing.T) {
	target := startReflectingTarget(t)
	calls := "    - method: " + missingMethod + "\n      rps: 50\n      duration: 300ms\n      data:\n        service: x\n"

	res := runCLI(t.Context(), t, 10*time.Second, "-c", dataConfig(t, target.addr, calls))
	if !errors.Is(res.err, descriptor.ErrMethodNotFound) || !strings.Contains(res.err.Error(), missingMethod) {
		t.Errorf("err = %v, want descriptor.ErrMethodNotFound naming %s", res.err, missingMethod)
	}
	if n := target.health.calls.Load(); n != 0 {
		t.Errorf("target received %d calls, want none before a config error", n)
	}
}

func TestRun_EveryDataErrorAtOnce(t *testing.T) {
	target := startReflectingTarget(t)
	calls := checkCall("      data:\n        first_bad: 1\n") +
		callWithData(unaryCallMethod, "        second_bad: 1\n")

	res := runCLI(t.Context(), t, 10*time.Second, "-c", dataConfig(t, target.addr, calls))
	if !errors.Is(res.err, cli.ErrRequestData) {
		t.Fatalf("err = %v, want cli.ErrRequestData", res.err)
	}
	for _, want := range []string{checkMethod, "first_bad", unaryCallMethod, "second_bad"} {
		if !strings.Contains(res.err.Error(), want) {
			t.Errorf("error %q misses %q: every problem should be reported at once", res.err, want)
		}
	}
}

// --- values that must arrive unchanged ------------------------------------

const (
	unaryCallMethod = "grpc.testing.TestService/UnaryCall"
	getSocketMethod = "grpc.channelz.v1.Channelz/GetSocket"
)

func callWithData(method, data string) string {
	return "    - method: " + method + "\n      rps: 20\n      duration: 200ms\n      data:\n" + data
}

func runData(t *testing.T, target liveTarget, method, data string) result {
	t.Helper()

	res := runCLI(t.Context(), t, 10*time.Second, "-c", dataConfig(t, target.addr, callWithData(method, data)))
	if res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}

	return res
}

func TestRun_Int64AboveTwoToTheFiftyThreeArrivesExactly(t *testing.T) {
	// 2^53 + 1. Any float64 on the way from YAML to protobuf rounds it to 2^53,
	// protojson accepts the rounded number without complaint, and the target
	// gets another ID. Snowflake IDs and amounts in satoshi or wei live here.
	const id int64 = 9_007_199_254_740_993

	target := startReflectingTarget(t)
	runData(t, target, getSocketMethod, "        socket_id: 9007199254740993\n")

	got := target.recorder.lastSocket()
	if got == nil || got.GetSocketId() != id {
		t.Errorf("target received socket_id = %v, want exactly %d", got.GetSocketId(), id)
	}
}

func TestRun_NestedMessageArrives(t *testing.T) {
	target := startReflectingTarget(t)
	runData(t, target, unaryCallMethod, "        response_size: 7\n        payload:\n          body: YWJjZA==\n")

	got := target.recorder.lastSimple()
	if got == nil || got.GetResponseSize() != 7 || string(got.GetPayload().GetBody()) != "abcd" {
		t.Errorf("target received %v, want response_size 7 and payload.body abcd", got)
	}
}

func TestRun_JSONFieldNameIsAccepted(t *testing.T) {
	target := startReflectingTarget(t)
	runData(t, target, unaryCallMethod, "        fillUsername: true\n")

	if got := target.recorder.lastSimple(); got == nil || !got.GetFillUsername() {
		t.Errorf("target received %v, want fill_username set through its JSON name", got)
	}
}

func TestRun_BytesAreBase64(t *testing.T) {
	// The protojson rule, pinned so the README stays true: bytes are written
	// in base64, and plain text that happens to be valid base64 decodes into
	// other bytes without an error. "abcd" is four characters of base64 and
	// three bytes of data.
	target := startReflectingTarget(t)
	runData(t, target, unaryCallMethod, "        payload:\n          body: abcd\n")

	got := target.recorder.lastSimple().GetPayload().GetBody()
	if want := []byte{0x69, 0xb7, 0x1d}; !bytes.Equal(got, want) {
		t.Errorf("body = %x, want %x: the base64 reading of abcd", got, want)
	}
}

func TestRun_UnknownEnumNameFailsBeforeTheRun(t *testing.T) {
	target := startReflectingTarget(t)
	cfg := dataConfig(t, target.addr, callWithData(unaryCallMethod, "        response_type: NO_SUCH_TYPE\n"))

	res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg)
	if !errors.Is(res.err, cli.ErrRequestData) || !strings.Contains(res.err.Error(), "response_type") {
		t.Errorf("err = %v, want cli.ErrRequestData naming response_type", res.err)
	}
}

// --- stopping -------------------------------------------------------------

// runStopped runs the fake target for a minute and presses stop n times once
// the run is under way.
func runStopped(t *testing.T, presses int, callLines string) result {
	t.Helper()
	return runSignalled(t, callLines, func(stops, _ chan<- struct{}) {
		for range presses {
			stops <- struct{}{}
		}
	})
}

// runSignalled runs the fake target for a minute and calls signal once the run
// is under way: stops carries Ctrl+C presses, aborts carries SIGTERM.
func runSignalled(t *testing.T, callLines string, signal func(stops, aborts chan<- struct{})) result {
	t.Helper()

	// Pressed only once calls are in flight, so an abort has something to cut.
	started := make(chan struct{})
	runStarting = func(eng *engine.Engine) {
		go func() {
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for range tick.C {
				if eng.Snapshot().InFlight > 0 {
					close(started)
					return
				}
			}
		}()
	}
	t.Cleanup(func() { runStarting = func(*engine.Engine) {} })

	cfg := fmt.Sprintf("app:\n  target:\n    ip: localhost\n    port: 1\nload:\n  calls:\n    - method: %s\n      rps: 50\n      duration: 1m\n%s",
		checkMethod, callLines)
	path := filepath.Join(t.TempDir(), "leettest.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	stops, aborts := make(chan struct{}), make(chan struct{})
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	args := []string{"-c", path, "-fake", "-fake-delay", "20s"}
	go func() { done <- run(t.Context(), stops, aborts, args, &stdout, &stderr) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not start")
	}
	go signal(stops, aborts)

	select {
	case err := <-done:
		return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the stop")
		return result{}
	}
}

func TestRun_StopDrainsPrintsTheReportAndIsIncomplete(t *testing.T) {
	// The fake answers in 20 s, the timeout is 300 ms: the drain ends on deadlines.
	res := runStopped(t, 1, "      timeout: 300ms\n")

	if !errors.Is(res.err, ErrIncomplete) {
		t.Errorf("err = %v, want ErrIncomplete: a stopped run must not exit 0", res.err)
	}
	if !strings.Contains(res.stdout, "incomplete") {
		t.Errorf("report does not say it is incomplete:\n%s", res.stdout)
	}
	if !strings.Contains(res.stderr, "stopping") {
		t.Errorf("stderr does not say the run is stopping:\n%s", res.stderr)
	}
}

func TestRun_AbortCountsCutOffCallsAndPrintsTheReport(t *testing.T) {
	res := runStopped(t, 2, "      timeout: 30s\n")

	if !errors.Is(res.err, ErrIncomplete) {
		t.Errorf("err = %v, want ErrIncomplete", res.err)
	}
	if !strings.Contains(res.stdout, "aborted") || !strings.Contains(res.stdout, "cut off by the abort") {
		t.Errorf("report does not count the aborted calls:\n%s", res.stdout)
	}
	if sent, failed := reportRow(t, res.stdout, checkMethod); sent == 0 || failed != 0 {
		t.Errorf("row sent %d failed %d, want the aborted calls in sent and none in failed", sent, failed)
	}
}

func TestRun_TerminateAbortsAtOnceWithTheReport(t *testing.T) {
	// The timeout is 30 s and runSignalled waits 10: a gentle drain would not
	// return in time, so returning at all means SIGTERM skipped it.
	res := runSignalled(t, "      timeout: 30s\n", func(_, aborts chan<- struct{}) {
		aborts <- struct{}{}
	})

	if !errors.Is(res.err, ErrIncomplete) {
		t.Errorf("err = %v, want ErrIncomplete", res.err)
	}
	if !strings.Contains(res.stdout, "cut off by the abort") {
		t.Errorf("report does not count the aborted calls:\n%s", res.stdout)
	}
	if strings.Contains(res.stderr, "Ctrl+C") {
		t.Errorf("SIGTERM comes from an orchestrator, yet stderr hints at Ctrl+C:%s", res.stderr)
	}
	if strings.Contains(res.stderr, "stopping") {
		t.Errorf("SIGTERM went through the gentle stop:\n%s", res.stderr)
	}
}

func TestRunResult_CapHitIsAnInvalidRunNotAnError(t *testing.T) {
	report := engine.Report{Incomplete: true, CapHit: &engine.CapHit{Unsent: 1}}
	err := runResult(report, fmt.Errorf("%w: 301", engine.ErrInFlightCapExceeded))

	if !errors.Is(err, ErrInvalidRun) {
		t.Errorf("err = %v, want ErrInvalidRun: the report and its verdict were printed", err)
	}
	if errors.Is(err, engine.ErrInFlightCapExceeded) {
		t.Errorf("err = %v: a cap hit is a verdict in the report, not an error printed without one", err)
	}
}

func TestRunResult_OtherFailuresStayErrors(t *testing.T) {
	boom := errors.New("boom")
	if err := runResult(engine.Report{}, boom); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure itself", err)
	}
}

// The advised timeout is the boundary: New accepts it and refuses one step
// more. Each call rounds its own rps × timeout up: dividing the cap by the
// summed rate once advised 600ms for 3 and 7 RPS under a cap of 10, which New
// then rejected with a budget of 11; a formula with a slot kept for rounding
// advised 1898ms where 1899ms fits.
func TestBudgetAdvice_TheAdviceIsTheBoundary(t *testing.T) {
	for _, maxInFlight := range []int{3, 10, 57, 500, 2000, 5000, 12345} {
		for _, rates := range [][]int{{1}, {50}, {3, 7}, {333}, {999, 1}, {1000}, {2500}, {100, 200, 300}} {
			calls := make([]engine.Call, len(rates))
			for i, rps := range rates {
				calls[i] = engine.Call{Method: strconv.Itoa(i), Timeout: 2 * time.Second,
					Stages: []engine.Stage{{StartRPS: rps, TargetRPS: rps, Duration: time.Second}}}
			}
			fresh := func(timeout time.Duration, cap int) error {
				for i := range calls {
					calls[i].Timeout = timeout
				}
				_, err := engine.New(engine.Options{Calls: calls, Sender: engine.FakeSender{}, MaxInFlight: cap})

				return err
			}

			var budget *engine.InFlightBudgetError
			if !errors.As(fresh(2*time.Second, maxInFlight), &budget) {
				continue
			}

			advice := withBudgetAdvice(budget, engine.Options{
				Calls: slices.Clone(calls), Sender: engine.FakeSender{}, MaxInFlight: maxInFlight,
			}).Error()
			if err := fresh(2*time.Second, budget.Need); err != nil {
				t.Errorf("cap %d, rates %v: advised -max-in-flight %d, New says %v", maxInFlight, rates, budget.Need, err)
			}

			at := strings.Index(advice, "at most ")
			if at < 0 {
				if !strings.Contains(advice, "no timeout fits") {
					t.Errorf("cap %d, rates %v: advice names neither a timeout nor why not: %q", maxInFlight, rates, advice)
				}
				continue
			}
			fits, err := time.ParseDuration(strings.Fields(advice[at+len("at most "):])[0])
			if err != nil {
				t.Fatalf("advice %q: %v", advice, err)
			}
			if fits <= 0 {
				t.Errorf("cap %d, rates %v: advised a timeout of %v", maxInFlight, rates, fits)
			}
			if err := fresh(fits, maxInFlight); err != nil {
				t.Errorf("cap %d, rates %v: advised %v, New says %v", maxInFlight, rates, fits, err)
			}
			step := time.Millisecond
			if fits < 2*time.Millisecond {
				step = time.Microsecond
			}
			if err := fresh(fits+step, maxInFlight); err == nil {
				t.Errorf("cap %d, rates %v: advised %v, yet %v fits too", maxInFlight, rates, fits, fits+step)
			}
		}
	}
}

// A pipeline reads the code, not the words: a config error, a run that stopped
// early and a run whose numbers say nothing must not look the same.
func TestExitCode_TellsTheOutcomesApart(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "plan finished", err: nil, want: 0},
		{name: "help", err: flag.ErrHelp, want: 0},
		{name: "config error", err: errors.New("cannot read config"), want: 1},
		{name: "invalid run", err: ErrInvalidRun, want: 2},
		{name: "invalid run, wrapped", err: fmt.Errorf("run: %w", ErrInvalidRun), want: 2},
		{name: "incomplete run", err: ErrIncomplete, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// 128 + the signal, the shell's own convention: a script that kills the run
// with SIGTERM must not read the result as "the person pressed Ctrl+C".
func TestExitCode_AnAbortedRunCarriesItsSignal(t *testing.T) {
	if got := abortCode(syscall.SIGTERM); got != 143 {
		t.Errorf("after SIGTERM = %d, want 143", got)
	}
	if got := abortCode(os.Interrupt); got != 130 {
		t.Errorf("after Ctrl+C = %d, want 130", got)
	}
	if got := abortCode(nil); got != 130 {
		t.Errorf("without a signal = %d, want 130", got)
	}
}

// A run can be both invalid and short: the cap ends it early. The codes are
// ranked, not summed — an invalid run's numbers do not describe the target at
// all, which is worse news than covering less of the plan.
func TestRunResult_AnInvalidRunOutranksAnIncompleteOne(t *testing.T) {
	err := runResult(engine.Report{Incomplete: true, CapHit: &engine.CapHit{Unsent: 1}}, engine.ErrInFlightCapExceeded)

	if got := exitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2: invalid outranks incomplete", got)
	}
	if !errors.Is(err, ErrInvalidRun) || errors.Is(err, ErrIncomplete) {
		t.Errorf("err = %v, want the invalid verdict alone", err)
	}
}

// The gentle stop prints a report, so it is an outcome of the run, not a kill:
// 130 and 143 belong to the exit that prints nothing at all.
func TestRun_AStoppedRunExitsWithTheIncompleteCodeNotASignalOne(t *testing.T) {
	res := runStopped(t, 1, "      timeout: 300ms\n")

	if got := exitCode(res.err); got != 3 {
		t.Errorf("exit code = %d, want 3: the run stopped early and printed its report (%v)", got, res.err)
	}
	if abortCode(os.Interrupt) == 3 {
		t.Fatal("the signal code equals the incomplete code: the test no longer separates them")
	}
	if !strings.Contains(res.stdout, "run finished") {
		t.Errorf("no report on stdout:\n%s", res.stdout)
	}
}

// Plenty of production services keep reflection off. A method without data
// needs no schema, so the run goes ahead; the warning says only that nothing
// could be checked in advance.
func TestRun_WithoutReflectionAMethodWithoutDataStillRuns(t *testing.T) {
	target := startTarget(t) // no reflection registered

	res := runCLI(t.Context(), t, 10*time.Second, "-c", writeConfig(t, target.addr, checkMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}

	sent, _ := reportRow(t, res.stdout, checkMethod)
	if sent == 0 {
		t.Fatal("nothing was sent against a target without reflection")
	}
	// In stderr before the run, and again with the report: a full-screen run
	// scrolls the first away, and the person reading the numbers is the one
	// who needs to know that nothing was checked.
	if !strings.Contains(res.stderr, "reflection is not enabled") || !strings.Contains(res.stderr, checkMethod) {
		t.Errorf("stderr does not warn about the unchecked method:\n%s", res.stderr)
	}
	if !strings.Contains(res.stdout, "not checked before the run") || !strings.Contains(res.stdout, checkMethod) ||
		!strings.Contains(res.stdout, "reflection is off") {
		t.Errorf("the report does not say the method was never checked:\n%s", res.stdout)
	}
}

// Ground: contract — a method whose every call was rejected measured how fast
// the target says no, not the target: for a pipeline that is a config error,
// the same class as a cap hit, and it outranks an early stop.
func TestRunResult_ARejectedMethodIsAnInvalidRun(t *testing.T) {
	rejected := engine.RefusalLatency{Count: 100}
	for _, tc := range []struct {
		name   string
		report engine.Report
		runErr error
		want   error
	}{
		{"one of three methods rejected outright", engine.Report{RequestRejected: true, Methods: []engine.MethodReport{
			{Method: "a.B/Good", Sent: 100}, {Method: "a.B/Also", Sent: 100},
			{Method: "a.B/Typo", Sent: 100, Failed: 100, Rejected: rejected},
		}}, nil, ErrInvalidRun},
		{"rejected and a cap hit", engine.Report{RequestRejected: true, CapHit: &engine.CapHit{Unsent: 1}},
			fmt.Errorf("%w: 301", engine.ErrInFlightCapExceeded), ErrInvalidRun},
		{"rejected and a soft stop", engine.Report{RequestRejected: true, Incomplete: true}, context.Canceled, ErrInvalidRun},
		{"one served call among 99 rejected", engine.Report{Methods: []engine.MethodReport{
			{Method: "a.B/One", Sent: 100, Failed: 99, Rejected: engine.RefusalLatency{Count: 99}},
		}}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runResult(tc.report, tc.runErr); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Ground: contract — a failed run's error goes to stderr as it came, in any
// script: the final screen's short verdict points there.
func TestPrintError_WritesTheErrorAsItCame(t *testing.T) {
	var out bytes.Buffer
	printError(&out, fmt.Errorf("connect to localhost:50051: %w", errors.New("соединение сброшено")))

	if got, want := out.String(), "leettest: connect to localhost:50051: соединение сброшено\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// Cut off changes no exit code: like unreachable before it, a call that got no
// status is a failure of the run's numbers, not a verdict on the run.
func TestRunResult_CutOffCallsKeepTheExitCode(t *testing.T) {
	for _, c := range []struct {
		name   string
		report engine.Report
	}{
		{"all unreachable", engine.Report{Sent: 10, Failed: 10, Methods: []engine.MethodReport{{Method: "a", Sent: 10, Failed: 10, Unanswered: 10}}}},
		{"all cut off", engine.Report{Sent: 10, Failed: 10, Methods: []engine.MethodReport{{Method: "a", Sent: 10, Failed: 10, CutOff: 10}}}},
	} {
		if err := runResult(c.report, nil); exitCode(err) != 0 {
			t.Errorf("%s: exit %d (%v), want 0", c.name, exitCode(err), err)
		}
	}
}
