package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/yhgrwav/leettest/pkg/config"
	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
)

// fakeResolver answers for the methods it knows and fails the rest the way
// the target's reflection would.
type fakeResolver struct {
	known map[string]protoreflect.MessageDescriptor
	err   error
	asked []string
}

func (r *fakeResolver) Resolve(_ context.Context, method string) (*descriptor.Method, error) {
	r.asked = append(r.asked, method)

	if r.err != nil {
		return nil, r.err
	}
	input, ok := r.known[method]
	if !ok {
		return nil, descriptor.ErrMethodNotFound
	}

	return &descriptor.Method{Path: "/" + method, Input: input}, nil
}

func (r *fakeResolver) ResolveAll(ctx context.Context, methods []string) (map[string]*descriptor.Method, error) {
	out := make(map[string]*descriptor.Method, len(methods))
	for _, m := range methods {
		resolved, err := r.Resolve(ctx, m)
		if err != nil {
			return nil, err
		}
		out[m] = resolved
	}

	return out, nil
}

func loadOf(methods ...config.Call) (*config.MasterConfig, []engine.Call) {
	cfg := &config.MasterConfig{Load: config.Load{Calls: methods}}
	calls := make([]engine.Call, len(methods))
	for i, c := range methods {
		calls[i] = engine.Call{Method: "/" + c.Method}
	}

	return cfg, calls
}

// A method nobody can serve costs a whole run to find out: every call comes
// back "no such method" and the report says nothing about load.
func TestAttachData_AMethodTheTargetDoesNotHaveIsRefusedBeforeTheRun(t *testing.T) {
	cfg, calls := loadOf(config.Call{Method: "wallet.v1.Wallet/Typo"})
	resolver := &fakeResolver{known: map[string]protoreflect.MessageDescriptor{}}

	var warn strings.Builder
	err := AttachData(t.Context(), resolver, cfg, calls, &warn)

	if !errors.Is(err, descriptor.ErrMethodNotFound) {
		t.Fatalf("error = %v, want %v", err, descriptor.ErrMethodNotFound)
	}
	if !strings.Contains(err.Error(), "wallet.v1.Wallet/Typo") {
		t.Errorf("error %q does not name the method", err)
	}
}

// Every method is checked, not only the ones carrying data.
func TestAttachData_ChecksEveryMethodEvenWithoutData(t *testing.T) {
	cfg, calls := loadOf(
		config.Call{Method: "wallet.v1.Wallet/One"},
		config.Call{Method: "wallet.v1.Wallet/Two"},
	)
	empty := (&emptypb.Empty{}).ProtoReflect().Descriptor()
	resolver := &fakeResolver{known: map[string]protoreflect.MessageDescriptor{
		"wallet.v1.Wallet/One": empty,
		"wallet.v1.Wallet/Two": empty,
	}}

	var warn strings.Builder
	if err := AttachData(t.Context(), resolver, cfg, calls, &warn); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(resolver.asked) != 2 {
		t.Errorf("resolved %v, want both methods", resolver.asked)
	}
}

// Without reflection a method with data cannot be built at all, while one
// without data runs as before: refusing it would take away a working case.
func TestAttachData_WithoutReflectionAMethodWithoutDataOnlyWarns(t *testing.T) {
	cfg, calls := loadOf(config.Call{Method: "wallet.v1.Wallet/One"})
	resolver := &fakeResolver{err: descriptor.ErrReflectionUnsupported}

	var warn strings.Builder
	if err := AttachData(t.Context(), resolver, cfg, calls, &warn); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !strings.Contains(warn.String(), "wallet.v1.Wallet/One") {
		t.Errorf("warning %q does not name the method left unchecked", warn.String())
	}
}

func TestAttachData_WithoutReflectionAMethodWithDataStillFails(t *testing.T) {
	cfg, calls := loadOf(config.Call{Method: "wallet.v1.Wallet/One", Data: map[string]any{"a": 1}})
	resolver := &fakeResolver{err: descriptor.ErrReflectionUnsupported}

	var warn strings.Builder
	if err := AttachData(t.Context(), resolver, cfg, calls, &warn); !errors.Is(err, descriptor.ErrReflectionUnsupported) {
		t.Fatalf("error = %v, want %v", err, descriptor.ErrReflectionUnsupported)
	}
}
