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

package descriptor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var (
	ErrReflectionUnsupported = errors.New("server reflection is not enabled on the target")
	ErrMethodNotFound        = errors.New("method not found on the target")
	ErrStreamingUnsupported  = errors.New("only unary methods are supported")
	ErrMalformedMethod       = errors.New(`method must look like "package.Service/Method"`)
)

type Method struct {
	Path   string
	Input  protoreflect.MessageDescriptor
	Output protoreflect.MessageDescriptor
}

// Payload is JSON. An unknown field is an error, so a typo in the config is caught here
// rather than silently dropped.
func (m *Method) NewRequest(payload []byte) (proto.Message, error) {
	msg := dynamicpb.NewMessage(m.Input)
	if len(payload) == 0 {
		return msg, nil
	}

	if err := (protojson.UnmarshalOptions{}).Unmarshal(payload, msg); err != nil {
		return nil, fmt.Errorf("build request for %s: %w", m.Path, err)
	}

	return msg, nil
}

func (m *Method) NewResponse() proto.Message {
	return dynamicpb.NewMessage(m.Output)
}

type Resolver interface {
	Resolve(ctx context.Context, method string) (*Method, error)
	ResolveAll(ctx context.Context, methods []string) (map[string]*Method, error)
}

type ReflectionResolver struct {
	conn grpc.ClientConnInterface
}

func NewReflectionResolver(cc grpc.ClientConnInterface) *ReflectionResolver {
	return &ReflectionResolver{conn: cc}
}

// Method is "package.Service/Method". Resolve talks to the server, so it belongs in
// startup, not on the hot path.
func (r *ReflectionResolver) Resolve(ctx context.Context, method string) (*Method, error) {
	resolved, err := r.ResolveAll(ctx, []string{method})
	if err != nil {
		return nil, err
	}

	return resolved[method], nil
}

// One reflection stream serves every method, so methods of the same service cost a
// single round trip and a single registry.
func (r *ReflectionResolver) ResolveAll(ctx context.Context, methods []string) (map[string]*Method, error) {
	client := grpcreflect.NewClientAuto(ctx, r.conn)
	defer client.Reset()

	resolved := make(map[string]*Method, len(methods))
	for _, method := range methods {
		if _, done := resolved[method]; done {
			continue
		}

		m, err := resolveOne(client, method)
		if err != nil {
			return nil, err
		}

		resolved[method] = m
	}

	return resolved, nil
}

func resolveOne(client *grpcreflect.Client, method string) (*Method, error) {
	service, name, err := splitMethod(method)
	if err != nil {
		return nil, err
	}

	sd, err := client.ResolveService(service)
	if err != nil {
		return nil, resolveError(err, service)
	}

	md := sd.FindMethodByName(name)
	if md == nil {
		return nil, fmt.Errorf("%w: %s", ErrMethodNotFound, method)
	}

	if md.IsClientStreaming() || md.IsServerStreaming() {
		return nil, fmt.Errorf("%w: %s", ErrStreamingUnsupported, method)
	}

	unwrapped := md.UnwrapMethod()

	return &Method{
		Path:   "/" + method,
		Input:  unwrapped.Input(),
		Output: unwrapped.Output(),
	}, nil
}

func splitMethod(method string) (service, name string, err error) {
	service, name, found := strings.Cut(method, "/")
	if !found || service == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("%w: got %q", ErrMalformedMethod, method)
	}

	if !strings.Contains(service, ".") {
		return "", "", fmt.Errorf("%w: service %q has no package", ErrMalformedMethod, method)
	}

	return service, name, nil
}

func resolveError(err error, service string) error {
	switch {
	case status.Code(err) == codes.Unimplemented:
		return ErrReflectionUnsupported
	case grpcreflect.IsElementNotFoundError(err):
		return fmt.Errorf("%w: service %s", ErrMethodNotFound, service)
	default:
		return fmt.Errorf("resolve %s: %w", service, err)
	}
}
