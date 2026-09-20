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

package grpcsender

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

const checkMethod = "/grpc.health.v1.Health/Check"

// target is a health service that answers however a test needs it to.
type target struct {
	grpc_health_v1.UnimplementedHealthServer

	code  codes.Code
	delay time.Duration
}

func (t *target) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if t.code != codes.OK {
		return nil, status.Error(t.code, "as the test asked")
	}

	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// dialTarget starts a health service on an in-process listener and returns a
// sender already connected to it.
func dialTarget(t *testing.T, srvTarget *target) *Sender {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, srvTarget)

	go func() { _ = srv.Serve(lis) }()

	sender := New(Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})

	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		_ = sender.Close()
		srv.Stop()
	})

	return sender
}

func request(scheduled time.Time) engine.Request {
	return engine.Request{Method: checkMethod, ScheduledAt: scheduled}
}

// --- connection ---------------------------------------------------------

func TestConnect_FailsOnUnreachableTargetAndNamesIt(t *testing.T) {
	sender := New(Options{Target: "127.0.0.1:1"})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	err := sender.Connect(ctx)
	if err == nil {
		t.Fatal("connect to a closed port succeeded, want an error before the run starts")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error %q does not name the address", err)
	}
}

func TestSend_BeforeConnectFails(t *testing.T) {
	sender := New(Options{Target: "127.0.0.1:1"})

	_, err := sender.Send(t.Context(), request(time.Now()))
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("err = %v, want ErrNotConnected", err)
	}
}

func TestSend_AfterCloseFails(t *testing.T) {
	sender := dialTarget(t, &target{})

	if err := sender.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err := sender.Send(t.Context(), request(time.Now()))
	if !errors.Is(err, ErrClosed) {
		t.Errorf("err = %v, want ErrClosed", err)
	}
}

func TestClose_IsSafeTwice(t *testing.T) {
	sender := dialTarget(t, &target{})

	if err := sender.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := sender.Close(); err != nil {
		t.Errorf("second close: %v, want nil", err)
	}
}

// --- sending ------------------------------------------------------------

func TestSend_EmptyPayloadIsValid(t *testing.T) {
	sender := dialTarget(t, &target{})

	out, err := sender.Send(t.Context(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v, want a message with no fields to be accepted", err)
	}
	if out.Category != engine.CategorySuccess {
		t.Errorf("category = %v, want success", out.Category)
	}
}

func TestSend_KeepsResponseOnlyWhenAsked(t *testing.T) {
	sender := dialTarget(t, &target{})

	kept, err := sender.Send(t.Context(), engine.Request{
		Method: checkMethod, ScheduledAt: time.Now(), KeepResponse: true,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(kept.Response) == 0 {
		t.Error("response is empty although KeepResponse was set")
	}

	dropped, err := sender.Send(t.Context(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if dropped.Response != nil {
		t.Errorf("response = %v, want nil when KeepResponse is off", dropped.Response)
	}
}

func TestSend_AppliesDeadlineAsGiven(t *testing.T) {
	sender := dialTarget(t, &target{delay: time.Second})

	scheduled := time.Now()
	req := request(scheduled)
	req.Deadline = scheduled.Add(100 * time.Millisecond)

	started := time.Now()

	out, err := sender.Send(t.Context(), req)
	if err != nil {
		t.Fatalf("send: %v, want a timeout to be a measurement, not a sender failure", err)
	}
	if out.Category != engine.CategoryTimeout {
		t.Errorf("category = %v, want timeout", out.Category)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("call took %v, want it abandoned near the deadline", elapsed)
	}
}

func TestSend_DeadlineInThePastTimesOutWithoutSending(t *testing.T) {
	sender := dialTarget(t, &target{delay: time.Second})

	scheduled := time.Now()
	req := request(scheduled)
	req.Deadline = scheduled.Add(-time.Second)

	out, err := sender.Send(t.Context(), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategoryTimeout {
		t.Errorf("category = %v, want timeout", out.Category)
	}
}

func TestSend_ZeroDeadlineMeansNoDeadline(t *testing.T) {
	sender := dialTarget(t, &target{delay: 50 * time.Millisecond})

	out, err := sender.Send(t.Context(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategorySuccess {
		t.Errorf("category = %v, want success: a zero deadline must not cut the call short", out.Category)
	}
}

// --- timestamps ---------------------------------------------------------

func TestSend_TimestampsComeFromTheTransport(t *testing.T) {
	const serverDelay = 80 * time.Millisecond

	sender := dialTarget(t, &target{delay: serverDelay})

	out, err := sender.Send(t.Context(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if out.SentAt.IsZero() || out.DoneAt.IsZero() {
		t.Fatalf("timestamps = %v / %v, want both filled", out.SentAt, out.DoneAt)
	}
	if !out.DoneAt.After(out.SentAt) {
		t.Errorf("DoneAt %v is not after SentAt %v", out.DoneAt, out.SentAt)
	}
	if service := out.DoneAt.Sub(out.SentAt); service < serverDelay {
		t.Errorf("service time = %v, want at least the server's %v", service, serverDelay)
	}
}

// --- classification -----------------------------------------------------

func TestSend_MapsStatusCodesToCategories(t *testing.T) {
	tests := []struct {
		code codes.Code
		want engine.Category
	}{
		{codes.OK, engine.CategorySuccess},
		{codes.InvalidArgument, engine.CategoryClientFault},
		{codes.NotFound, engine.CategoryClientFault},
		{codes.PermissionDenied, engine.CategoryClientFault},
		{codes.Unauthenticated, engine.CategoryClientFault},
		{codes.FailedPrecondition, engine.CategoryClientFault},
		{codes.Unimplemented, engine.CategoryClientFault},
		{codes.Internal, engine.CategoryServerFault},
		{codes.Unknown, engine.CategoryServerFault},
		{codes.DataLoss, engine.CategoryServerFault},
		{codes.ResourceExhausted, engine.CategoryOverload},
		// A served UNAVAILABLE is a reply, unlike a refused connection.
		{codes.Unavailable, engine.CategoryOverload},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			sender := dialTarget(t, &target{code: tt.code})

			out, err := sender.Send(t.Context(), request(time.Now()))
			if err != nil {
				t.Fatalf("send: %v, want a target error to be a measurement", err)
			}
			if out.Category != tt.want {
				t.Errorf("category = %v, want %v", out.Category, tt.want)
			}
			if tt.code != codes.OK && out.Err == nil {
				t.Error("Err is nil although the call failed")
			}
		})
	}
}

func TestSend_RefusedConnectionIsNotAMeasurement(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, &target{})

	go func() { _ = srv.Serve(lis) }()

	sender := New(Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	// The target goes away mid-run: the call carries no latency worth recording.
	srv.Stop()
	lis.Close()

	out, err := sender.Send(t.Context(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v, want an unreachable target to be data, not a broken sender", err)
	}
	if out.Category != engine.CategoryUnreachable {
		t.Errorf("category = %v, want unreachable: no reply ever came", out.Category)
	}
}

func TestSend_CancellationWrapsContextError(t *testing.T) {
	sender := dialTarget(t, &target{delay: time.Second})

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	_, err := sender.Send(ctx, request(time.Now()))
	if err == nil {
		t.Fatal("cancelled call returned no error")
	}
	// grpc-go does not wrap ctx.Err(), so the sender must: without it the engine
	// cannot tell a deliberate stop from a broken sender, and Ctrl+C turns into
	// a failed run.
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// --- concurrency --------------------------------------------------------

func TestSend_IsSafeUnderConcurrentUse(t *testing.T) {
	const callers = 200

	sender := dialTarget(t, &target{})

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures []error
	)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			out, err := sender.Send(t.Context(), request(time.Now()))
			if err == nil && out.SentAt.IsZero() {
				err = errors.New("SentAt is zero")
			}
			if err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(failures) > 0 {
		t.Errorf("%d of %d concurrent calls failed, first: %v", len(failures), callers, failures[0])
	}
}
