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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/stats"
)

const (
	checkMethod   = "grpc.health.v1.Health/Check"
	missingMethod = "grpc.health.v1.Health/DoesNotExist"
)

// health answers every Check and counts what reached it.
type health struct {
	grpc_health_v1.UnimplementedHealthServer

	calls atomic.Int64
}

func (h *health) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	h.calls.Add(1)

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

type liveTarget struct {
	addr   string
	health *health
	ended  <-chan struct{}
}

// startTarget runs a plaintext health service on a real TCP port, because the
// CLI dials an address from the config and cannot be handed a dialer.
func startTarget(t *testing.T) liveTarget {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	h := &health{}
	ends := &connEnds{done: make(chan struct{})}
	srv := grpc.NewServer(grpc.StatsHandler(ends))
	grpc_health_v1.RegisterHealthServer(srv, h)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return liveTarget{addr: lis.Addr().String(), health: h, ended: ends.done}
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
`, host, port, tlsLine, method)

	path := filepath.Join(t.TempDir(), "loadgen.yaml")
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
	go func() { done <- run(ctx, args, &stdout, &stderr) }()

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

	res := runCLI(t.Context(), t, 10*time.Second, "-c", writeConfig(t, target.addr, missingMethod, plaintext))
	if res.err != nil {
		t.Fatalf("run: %v, want the run to finish and report the failures", res.err)
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
