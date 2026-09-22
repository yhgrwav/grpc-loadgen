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

package descriptor_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/yhgrwav/leettest/pkg/descriptor"
)

const (
	unaryMethod     = "grpc.health.v1.Health/Check"
	listMethod      = "grpc.health.v1.Health/List"
	streamingMethod = "grpc.health.v1.Health/Watch"
)

func dialTarget(t *testing.T, withReflection bool) grpc.ClientConnInterface {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())

	if withReflection {
		reflection.Register(server)
	}

	go func() {
		_ = server.Serve(listener)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	})

	return conn
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveUnaryMethod(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	method, err := resolver.Resolve(t.Context(), unaryMethod)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "/" + unaryMethod; method.Path != want {
		t.Errorf("path = %q, want %q", method.Path, want)
	}

	if got, want := string(method.Input.FullName()), "grpc.health.v1.HealthCheckRequest"; got != want {
		t.Errorf("input = %q, want %q", got, want)
	}

	if got, want := string(method.Output.FullName()), "grpc.health.v1.HealthCheckResponse"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveAllDeduplicates(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	resolved, err := resolver.ResolveAll(t.Context(), []string{unaryMethod, unaryMethod, listMethod})
	if err != nil {
		t.Fatalf("resolve all: %v", err)
	}

	if len(resolved) != 2 {
		t.Fatalf("resolved %d methods, want 2", len(resolved))
	}

	for _, method := range []string{unaryMethod, listMethod} {
		if resolved[method] == nil {
			t.Errorf("%s missing from result", method)
		}
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveAllFailsOnFirstBadMethod(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	_, err := resolver.ResolveAll(t.Context(), []string{unaryMethod, "grpc.health.v1.Health/Nope"})
	if !errors.Is(err, descriptor.ErrMethodNotFound) {
		t.Fatalf("err = %v, want ErrMethodNotFound", err)
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveRejectsStreaming(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	_, err := resolver.Resolve(t.Context(), streamingMethod)
	if !errors.Is(err, descriptor.ErrStreamingUnsupported) {
		t.Fatalf("err = %v, want ErrStreamingUnsupported", err)
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveMissingMethod(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	_, err := resolver.Resolve(t.Context(), "grpc.health.v1.Health/Nope")
	if !errors.Is(err, descriptor.ErrMethodNotFound) {
		t.Fatalf("err = %v, want ErrMethodNotFound", err)
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveMissingService(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	_, err := resolver.Resolve(t.Context(), "nope.Service/Method")
	if !errors.Is(err, descriptor.ErrMethodNotFound) {
		t.Fatalf("err = %v, want ErrMethodNotFound", err)
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveWithoutReflection(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, false))

	_, err := resolver.Resolve(t.Context(), unaryMethod)
	if !errors.Is(err, descriptor.ErrReflectionUnsupported) {
		t.Fatalf("err = %v, want ErrReflectionUnsupported", err)
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestResolveMalformedMethod(t *testing.T) {
	cases := map[string]string{
		"no slash":       "grpc.health.v1.Health.Check",
		"no package":     "Health/Check",
		"empty service":  "/Check",
		"empty method":   "grpc.health.v1.Health/",
		"two slashes":    "grpc.health.v1.Health/Check/Extra",
		"empty entirely": "",
	}

	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	for name, method := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := resolver.Resolve(t.Context(), method)
			if !errors.Is(err, descriptor.ErrMalformedMethod) {
				t.Fatalf("err = %v, want ErrMalformedMethod", err)
			}
		})
	}
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestNewRequest(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	method, err := resolver.Resolve(t.Context(), unaryMethod)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	t.Run("fills fields", func(t *testing.T) {
		msg, err := method.NewRequest([]byte(`{"service":"billing"}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		encoded, err := protojson.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		if got, want := string(encoded), `{"service":"billing"}`; got != want {
			t.Errorf("payload = %s, want %s", got, want)
		}
	})

	t.Run("empty payload yields empty message", func(t *testing.T) {
		msg, err := method.NewRequest(nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		if got := msg.ProtoReflect().Descriptor().FullName(); got != method.Input.FullName() {
			t.Errorf("descriptor = %s, want %s", got, method.Input.FullName())
		}
	})

	t.Run("unknown field is an error", func(t *testing.T) {
		if _, err := method.NewRequest([]byte(`{"nosuchfield":1}`)); err == nil {
			t.Fatal("want error for unknown field, got nil")
		}
	})
}

// Ground: contract — pkg/descriptor is a library API; callers get this without our CLI.
func TestNewResponse(t *testing.T) {
	resolver := descriptor.NewReflectionResolver(dialTarget(t, true))

	method, err := resolver.Resolve(t.Context(), unaryMethod)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	msg := method.NewResponse()
	if got := msg.ProtoReflect().Descriptor().FullName(); got != method.Output.FullName() {
		t.Errorf("descriptor = %s, want %s", got, method.Output.FullName())
	}
}
