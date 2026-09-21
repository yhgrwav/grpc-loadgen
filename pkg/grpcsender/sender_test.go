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
	"sync/atomic"
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
	calls atomic.Int64
}

func (t *target) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	t.calls.Add(1)

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

// connectWithin runs Connect and fails the test if it has not returned by
// limit: the property under test is that Connect gives up on its own, so the
// ceiling lives outside the ctx that Connect sees.
func connectWithin(ctx context.Context, t *testing.T, sender *Sender, limit time.Duration) error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- sender.Connect(ctx) }()

	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Connect has not returned after %v", limit)

		return nil
	}
}

// closedPort returns an address nothing listens on: the port was just taken
// and released, so a dial there is refused immediately.
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

// withoutDeadline is a ctx the way a careless caller would pass it: no
// deadline, cancelled only when the test ends so a hanging Connect does not
// outlive it.
func withoutDeadline(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return ctx
}

func TestConnect_RefusedAddressFailsWithoutADeadline(t *testing.T) {
	addr := closedPort(t)
	sender := New(Options{Target: addr})
	t.Cleanup(func() { _ = sender.Close() })

	err := connectWithin(withoutDeadline(t), t, sender, 3*time.Second)
	if err == nil {
		t.Fatal("connect to a closed port succeeded, want an error before the run starts")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Errorf("error %q does not name the address %s", err, addr)
	}
}

func TestConnect_RefusedAddressCarriesTheTransportCause(t *testing.T) {
	addr := closedPort(t)
	sender := New(Options{Target: addr})
	t.Cleanup(func() { _ = sender.Close() })

	err := connectWithin(withoutDeadline(t), t, sender, 3*time.Second)
	if err == nil {
		t.Fatal("connect to a closed port succeeded")
	}
	// The cause text comes from the OS and may be localized, so the test checks
	// that one is there, not what it says.
	_, cause, found := strings.Cut(err.Error(), addr)
	if !found {
		t.Fatalf("error %q does not name the address %s", err, addr)
	}
	if strings.TrimLeft(cause, ": ") == "" {
		t.Errorf("error %q names the address but not why the connection failed", err)
	}
}

func TestConnect_UnresolvableNameFailsWithoutADeadline(t *testing.T) {
	// .invalid is reserved by RFC 2606 and never resolves.
	const addr = "no-such-host.invalid:443"

	sender := New(Options{Target: addr})
	t.Cleanup(func() { _ = sender.Close() })

	err := connectWithin(withoutDeadline(t), t, sender, 5*time.Second)
	if err == nil {
		t.Fatal("connect to an unresolvable name succeeded")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Errorf("error %q does not name the address %s", err, addr)
	}
}

func TestTransportCause_ProbeThatReachesATargetHasNoEffect(t *testing.T) {
	// The race the probe is built for: the connection came up between seeing
	// TRANSIENT_FAILURE and probing. The target must see nothing it would act
	// on, and the probe must report the connection as usable.
	srv := &target{}
	sender := dialTarget(t, srv)

	if cause := transportCause(bounded(t), sender.conn); cause != nil {
		t.Errorf("cause = %v, want nil: the target answered, so the connection works", cause)
	}
	if n := srv.calls.Load(); n != 0 {
		t.Errorf("target served %d real calls, want none from a probe", n)
	}
}

func TestTransportCause_AnyServedStatusMeansTheConnectionWorks(t *testing.T) {
	// Many services check credentials before routing, so an unknown method
	// comes back as whatever the interceptor says, not Unimplemented. A status
	// from the target proves the connection works whatever the code.
	for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			lis := bufconn.Listen(1024 * 1024)
			srv := grpc.NewServer(grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
				return status.Error(code, "rejected before routing")
			}))

			go func() { _ = srv.Serve(lis) }()
			t.Cleanup(srv.Stop)

			sender := New(Options{
				Target: "passthrough:///bufnet",
				DialOptions: []grpc.DialOption{
					grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
						return lis.DialContext(ctx)
					}),
				},
			})
			if err := sender.Connect(bounded(t)); err != nil {
				t.Fatalf("connect: %v", err)
			}
			t.Cleanup(func() { _ = sender.Close() })

			if cause := transportCause(bounded(t), sender.conn); cause != nil {
				t.Errorf("cause = %v, want nil: the target answered %s", cause, code)
			}
		})
	}
}

func TestConnect_CallerDeadlineBoundsASilentTarget(t *testing.T) {
	// Accepts TCP and never speaks: the handshake neither completes nor fails,
	// so only the caller's deadline can end the wait.
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

	sender := New(Options{Target: lis.Addr().String()})
	t.Cleanup(func() { _ = sender.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	err = connectWithin(ctx, t, sender, 3*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want errors.Is(err, context.DeadlineExceeded)", err)
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

func TestSend_AcceptsTheMethodAsWrittenInTheConfig(t *testing.T) {
	// The config names a method without the leading slash of a gRPC path.
	sender := dialTarget(t, &target{})

	out, err := sender.Send(bounded(t), engine.Request{
		Method: strings.TrimPrefix(checkMethod, "/"), ScheduledAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategorySuccess {
		t.Errorf("category = %v (%v), want success", out.Category, out.Err)
	}
}

func TestSend_MethodPathCostsNoAllocationPerRequest(t *testing.T) {
	sender := New(Options{})

	for _, method := range []string{checkMethod, strings.TrimPrefix(checkMethod, "/")} {
		sender.path(method)

		if allocs := testing.AllocsPerRun(100, func() { _ = sender.path(method) }); allocs != 0 {
			t.Errorf("path(%q) allocates %.0f times per call, want 0 on the hot path", method, allocs)
		}
		if got := sender.path(method); got != checkMethod {
			t.Errorf("path(%q) = %q, want %q", method, got, checkMethod)
		}
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
	srv := &target{delay: time.Second}
	sender := dialTarget(t, srv)

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
	if n := srv.calls.Load(); n != 0 {
		t.Errorf("target received %d calls, want none: the request had no budget left", n)
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

func TestSend_MegabytePayloadGoesThrough(t *testing.T) {
	// A HealthCheckRequest whose only field, service (field 1, a string), is a
	// megabyte long: valid on the wire, and big enough to span many frames.
	name := make([]byte, 1<<20)
	for i := range name {
		name[i] = 'a'
	}

	payload := []byte{0x0a}
	for n := len(name); ; n >>= 7 {
		if n < 0x80 {
			payload = append(payload, byte(n))

			break
		}
		payload = append(payload, byte(n&0x7f|0x80))
	}
	payload = append(payload, name...)

	sender := dialTarget(t, &target{})

	req := request(time.Now())
	req.Payload = payload

	out, err := sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategorySuccess {
		t.Errorf("category = %v (%v), want success", out.Category, out.Err)
	}
}

func TestSend_ContextCancelledBeforeTheCallWrapsItsError(t *testing.T) {
	sender := dialTarget(t, &target{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := sender.Send(ctx, request(time.Now()))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func TestConnect_TLSFlagTurnsOnTransportCredentials(t *testing.T) {
	// The target speaks plaintext; a sender asked for TLS must fail the
	// handshake instead of quietly falling back to an insecure connection.
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, &target{})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	sender := New(Options{
		Target: "passthrough:///bufnet",
		TLS:    true,
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})
	t.Cleanup(func() { _ = sender.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if err := sender.Connect(ctx); err == nil {
		t.Fatal("TLS sender connected to a plaintext server, want the handshake to fail")
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
		// A server that cancels the call on its own side broke off the work.
		{codes.Canceled, engine.CategoryServerFault},
		{codes.ResourceExhausted, engine.CategoryOverload},
		// A served UNAVAILABLE is a reply, unlike a refused connection.
		{codes.Unavailable, engine.CategoryOverload},
		// ABORTED means a concurrency conflict, which is what load produces:
		// blaming the caller for it would turn a load signal into a config error.
		{codes.Aborted, engine.CategoryOverload},
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
			if (out.Err == nil) != (tt.code == codes.OK) {
				t.Errorf("Err = %v, want nil exactly when the call succeeded", out.Err)
			}
		})
	}
}

// vanishedTarget returns a sender that connected to a live target which then
// went away: calls still go out, and nothing is there to answer them.
func vanishedTarget(t *testing.T) *Sender {
	t.Helper()

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

	srv.Stop()
	_ = lis.Close()

	return sender
}

// bounded gives a call a ceiling, so a regression fails the test instead of
// eating the CI timeout.
func bounded(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	return ctx
}

func TestSend_SameCodeFromTwoSourcesGetsDifferentCategories(t *testing.T) {
	// The whole point of watching InTrailer: UNAVAILABLE from a server that
	// answered is a measurement, UNAVAILABLE from a target nobody reached is
	// not. The status code alone cannot tell them apart.
	served, err := dialTarget(t, &target{code: codes.Unavailable}).Send(bounded(t), request(time.Now()))
	if err != nil {
		t.Fatalf("send to answering target: %v", err)
	}

	refused, err := vanishedTarget(t).Send(bounded(t), request(time.Now()))
	if err != nil {
		t.Fatalf("send to vanished target: %v", err)
	}

	if served.Code != refused.Code {
		t.Fatalf("codes differ (%q vs %q): the test no longer pins the same code from two sources",
			served.Code, refused.Code)
	}
	if served.Category != engine.CategoryOverload {
		t.Errorf("served %s = %v, want overload: the target did reply", served.Code, served.Category)
	}
	if refused.Category != engine.CategoryUnreachable {
		t.Errorf("refused %s = %v, want unreachable: nothing replied", refused.Code, refused.Category)
	}
}

func TestSend_ReportsTheRawTransportCode(t *testing.T) {
	// The category is a guess where UNAVAILABLE is concerned; the raw code is a
	// fact, and the report shows both.
	sender := dialTarget(t, &target{code: codes.Unavailable})

	out, err := sender.Send(bounded(t), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Code != codes.Unavailable.String() {
		t.Errorf("code = %q, want %q", out.Code, codes.Unavailable.String())
	}
}

func TestSend_RefusedConnectionIsNotAMeasurement(t *testing.T) {
	out, err := vanishedTarget(t).Send(bounded(t), request(time.Now()))
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
	const callers = 1000

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
