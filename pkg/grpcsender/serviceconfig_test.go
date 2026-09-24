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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// methodConfig is a service config a target could hand out through its
// resolver, with one field set for the health service.
func methodConfig(field string) string {
	return `{"methodConfig": [{"name": [{"service": "grpc.health.v1.Health"}], ` + field + `}]}`
}

// resolvedTarget serves the health service at two addresses, answering after
// delay, and hands out both with config through a manual resolver.
type resolvedTarget struct {
	sender   *Sender
	accepted atomic.Int32

	mu   sync.Mutex
	down bool
	lis  map[string]*bufconn.Listener
	srvs []*grpc.Server
}

func resolved(t *testing.T, delay time.Duration, config string) *resolvedTarget {
	t.Helper()

	r := &resolvedTarget{lis: map[string]*bufconn.Listener{}}
	var addrs []resolver.Address

	for _, name := range []string{"a:1", "b:1"} {
		lis := bufconn.Listen(1024 * 1024)
		srv := grpc.NewServer()
		grpc_health_v1.RegisterHealthServer(srv, servingTarget{delay: delay})

		go func() { _ = srv.Serve(lis) }()

		r.lis[name] = lis
		r.srvs = append(r.srvs, srv)
		addrs = append(addrs, resolver.Address{Addr: name})
	}
	t.Cleanup(r.stop)

	res := manual.NewBuilderWithScheme("leettest-sc")
	res.BuildCallback = func(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) {
		go func() {
			_ = cc.UpdateState(resolver.State{Addresses: addrs, ServiceConfig: cc.ParseServiceConfig(config)})
		}()
	}

	r.sender = New(Options{Target: "leettest-sc:///target", DialOptions: []grpc.DialOption{
		grpc.WithResolvers(res),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			r.mu.Lock()
			down, lis := r.down, r.lis[addr]
			r.mu.Unlock()

			if down || lis == nil {
				return nil, errors.New("target is down")
			}
			r.accepted.Add(1)

			return lis.DialContext(ctx)
		}),
	}})
	t.Cleanup(func() { _ = r.sender.Close() })

	if err := r.sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	return r
}

func (r *resolvedTarget) stop() {
	r.mu.Lock()
	r.down = true
	r.mu.Unlock()

	for _, srv := range r.srvs {
		srv.Stop()
	}
}

func (r *resolvedTarget) send(t *testing.T, deadline time.Duration) (engine.Outcome, time.Duration) {
	t.Helper()

	req := request(time.Now())
	req.Deadline = req.ScheduledAt.Add(deadline)

	start := time.Now()
	out, err := r.sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	return out, time.Since(start)
}

// Ground: signal grpc-go v1.84.0 — a service config from the resolver may pick round_robin, which
// opens a connection to every address; the report says one connection and the stream verdict of
// B1 counts on one. The sender must keep pick_first whatever the target hands out.
func TestServiceConfig_FromTheResolverDoesNotOpenMoreConnections(t *testing.T) {
	r := resolved(t, 0, `{"loadBalancingPolicy": "round_robin"}`)

	for range 10 {
		r.send(t, time.Second)
	}

	if n := r.accepted.Load(); n != 1 {
		t.Errorf("%d connections dialed, want 1", n)
	}
}

// Ground: signal grpc-go v1.84.0 — a method timeout in the service config takes the smaller of it
// and ours, so a call the target answers in 200ms would count as our timeout at 50ms.
func TestServiceConfig_FromTheResolverDoesNotShortenOurDeadline(t *testing.T) {
	r := resolved(t, 200*time.Millisecond, methodConfig(`"timeout": "0.05s"`))

	if out, _ := r.send(t, time.Second); out.Category != engine.CategorySuccess {
		t.Errorf("category %v (%v), want success: our deadline is 1s, the target answers in 200ms", out.Category, out.Err)
	}
}

// Ground: signal grpc-go v1.84.0 — waitForReady from the service config holds a call on a failed
// connection until its deadline, turning an unreachable target into calls not sent.
func TestServiceConfig_FromTheResolverDoesNotHoldCallsOnAFailedConnection(t *testing.T) {
	r := resolved(t, 0, methodConfig(`"waitForReady": true`))
	r.stop()

	// pick_first rests in IDLE after losing its connection; asking it to
	// connect to a target that is down puts it in TRANSIENT_FAILURE.
	conn := r.sender.conn
	for conn.GetState() != connectivity.TransientFailure {
		conn.Connect()
		if !conn.WaitForStateChange(bounded(t), conn.GetState()) {
			t.Fatal("the connection never failed")
		}
	}

	out, took := r.send(t, time.Second)
	if out.NotSent || out.Category != engine.CategoryUnreachable {
		t.Errorf("not sent %v, category %v, want unreachable at once", out.NotSent, out.Category)
	}
	if took > 500*time.Millisecond {
		t.Errorf("the call took %v on a failed connection; the deadline is 1s", took)
	}
}

// Ground: signal grpc-go v1.84.0 — maxResponseMessageBytes from the service config refuses replies
// locally; the reply size is ours to set (app.max_response_size), not the target's resolver's.
func TestServiceConfig_FromTheResolverDoesNotCapTheReply(t *testing.T) {
	r := resolved(t, 0, methodConfig(`"maxResponseMessageBytes": 1`))

	if out, _ := r.send(t, time.Second); out.Category != engine.CategorySuccess {
		t.Errorf("category %v (%v), want success: the reply is two bytes", out.Category, out.Err)
	}
}

// servingTarget answers SERVING after delay: a two-byte reply.
type servingTarget struct {
	grpc_health_v1.UnimplementedHealthServer

	delay time.Duration
}

func (s servingTarget) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}
