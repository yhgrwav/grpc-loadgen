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

	"google.golang.org/grpc"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

var (
	ErrNotConnected = errors.New("sender is not connected, call Connect before the run")
	ErrClosed       = errors.New("sender is closed")

	// errNotImplemented keeps the package compiling while the specification is
	// under review, so the tests written from it fail with a readable message
	// instead of a panic. It goes away with the implementation.
	errNotImplemented = errors.New("grpcsender: not implemented yet")
)

// Options describes the target and how to reach it.
type Options struct {
	// Target is the address to dial, as host:port.
	Target string
	// TLS turns on transport credentials; without it the connection is
	// insecure, which is the usual case for a service behind a mesh.
	TLS bool
	// DialOptions are passed through for cases the fields above do not cover,
	// such as custom credentials or an in-process dialer in tests.
	DialOptions []grpc.DialOption
}

// Sender delivers calls to a real gRPC target.
type Sender struct {
	opts Options
}

// New prepares a sender. It does not dial: the connection is established by
// Connect, before the run starts, so a wrong address fails immediately rather
// than a minute into the load.
func New(opts Options) *Sender {
	return &Sender{opts: opts}
}

// Connect establishes the connection to the target.
func (s *Sender) Connect(ctx context.Context) error {
	return errNotImplemented
}

// Close releases the connection. Calling it twice is safe.
func (s *Sender) Close() error {
	return errNotImplemented
}

// Send performs one call. See engine.Sender for what the two failure channels
// mean.
func (s *Sender) Send(ctx context.Context, req engine.Request) (engine.Outcome, error) {
	return engine.Outcome{}, errNotImplemented
}
