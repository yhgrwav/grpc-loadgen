package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	_, err := AttachData(t.Context(), resolver, cfg, calls)

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

	if _, err := AttachData(t.Context(), resolver, cfg, calls); err != nil {
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

	unchecked, err := AttachData(t.Context(), resolver, cfg, calls)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(unchecked) != 1 || unchecked[0].Method != "wallet.v1.Wallet/One" {
		t.Fatalf("unchecked = %v, want the one method nothing could be checked against", unchecked)
	}
	if !errors.Is(unchecked[0].Err, descriptor.ErrReflectionUnsupported) {
		t.Errorf("reason = %v, want the reflection error itself", unchecked[0].Err)
	}
}

func TestAttachData_WithoutReflectionAMethodWithDataStillFails(t *testing.T) {
	cfg, calls := loadOf(config.Call{Method: "wallet.v1.Wallet/One", Data: map[string]any{"a": 1}})
	resolver := &fakeResolver{err: descriptor.ErrReflectionUnsupported}

	if _, err := AttachData(t.Context(), resolver, cfg, calls); !errors.Is(err, descriptor.ErrReflectionUnsupported) {
		t.Fatalf("error = %v, want %v", err, descriptor.ErrReflectionUnsupported)
	}
}

// Reflection can fail for reasons other than being off: it may want
// credentials, or not answer in time. A method without data still runs — but
// calling that "reflection is off" would send the reader to the wrong place.
func TestAttachData_ReflectionRefusedIsNotTheSameAsReflectionOff(t *testing.T) {
	cfg, calls := loadOf(config.Call{Method: "wallet.v1.Wallet/One"})
	refused := status.Error(codes.PermissionDenied, "reflection needs a token")
	resolver := &fakeResolver{err: refused}

	unchecked, err := AttachData(t.Context(), resolver, cfg, calls)
	if err != nil {
		t.Fatalf("attach: %v, want the run to go on", err)
	}
	if len(unchecked) != 1 {
		t.Fatalf("unchecked = %v, want the method", unchecked)
	}
	if errors.Is(unchecked[0].Err, descriptor.ErrReflectionUnsupported) {
		t.Errorf("a refusal was read as reflection being off: %v", unchecked[0].Err)
	}

	var out strings.Builder
	PrintUnchecked(&out, unchecked)

	if strings.Contains(out.String(), "is off") {
		t.Errorf("the report says reflection is off, though it was refused:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "PermissionDenied") {
		t.Errorf("the report does not say why it could not be used:\n%s", out.String())
	}
}
