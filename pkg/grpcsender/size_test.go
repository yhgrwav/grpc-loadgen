package grpcsender

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// dialWith serves target under srvOpts and connects with callOpts, so a test
// can put a message-size limit on either side.
func dialWith(t testing.TB, srvOpts []grpc.ServerOption, callOpts ...grpc.DialOption) *Sender {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(srvOpts...)
	grpc_health_v1.RegisterHealthServer(srv, &target{})

	go func() { _ = srv.Serve(lis) }()

	dial := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	}, callOpts...)

	sender := New(Options{Target: "passthrough:///bufnet", DialOptions: dial})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = sender.Close()
		srv.Stop()
	})

	return sender
}

// Ground: signal grpc-go v1.84.0 — the client cuts a reply over its size limit
// off before the trailer, so nothing says the target answered. Read as
// "unreachable" it would drop the call out of the numbers altogether, and read
// as overload it would blame the load; the request simply does not fit.
func TestSend_AReplyOverTheClientLimitIsARequestError(t *testing.T) {
	sender := dialWith(t, nil, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1)))

	out, err := sender.Send(t.Context(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategoryClientFault {
		t.Errorf("category = %v, want %v (%v)", out.Category, engine.CategoryClientFault, out.Err)
	}
	if !strings.Contains(out.Err.Error(), "larger than max") {
		t.Fatalf("error = %v, want the size limit: the test no longer pins that path", out.Err)
	}
}

// Ground: signal grpc-go v1.84.0 — a server over its own receive limit answers
// RESOURCE_EXHAUSTED, the same code a target out of capacity returns. The size
// message is what tells them apart, and the run's verdict depends on it: a
// request that does not fit will not fit at any rate.
func TestSend_ARequestOverTheServerLimitIsARequestError(t *testing.T) {
	sender := dialWith(t, []grpc.ServerOption{grpc.MaxRecvMsgSize(1)})

	// A health check with a service name of its own is longer than one byte.
	req := request(time.Now())
	req.Payload = []byte{0x0a, 0x04, 'b', 'i', 'g', '!'}

	out, err := sender.Send(t.Context(), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategoryClientFault {
		t.Errorf("category = %v, want %v (%v)", out.Category, engine.CategoryClientFault, out.Err)
	}
}

// Ground: boundary — the code that means both "out of capacity" and "does not
// fit": only the size limit is a request error.
func TestSend_PlainResourceExhaustedStaysOverload(t *testing.T) {
	sender := dialTarget(t, &target{code: codes.ResourceExhausted})

	out, err := sender.Send(context.Background(), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out.Category != engine.CategoryOverload {
		t.Errorf("category = %v, want %v", out.Category, engine.CategoryOverload)
	}
}
