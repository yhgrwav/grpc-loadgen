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
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

var (
	ErrNotConnected = errors.New("sender is not connected, call Connect before the run")
	ErrClosed       = errors.New("sender is closed")
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

	mu     sync.RWMutex
	conn   *grpc.ClientConn
	closed bool
}

// New prepares a sender. It does not dial: the connection is established by
// Connect, before the run starts, so a wrong address fails immediately rather
// than a minute into the load.
func New(opts Options) *Sender {
	return &Sender{opts: opts}
}

// Connect establishes the connection and waits for it to become usable, so an
// unreachable target is reported before any load is scheduled rather than as a
// wall of failures once the run is under way.
//
// ctx bounds the wait. gRPC keeps retrying an unreachable target on its own, so
// a ctx without a deadline turns a mistyped address into a hang instead of an
// error: give it one.
func (s *Sender) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrClosed
	}
	if s.conn != nil {
		return nil
	}

	creds := insecure.NewCredentials()
	if s.opts.TLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithStatsHandler(handler{}),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	}, s.opts.DialOptions...)

	conn, err := grpc.NewClient(s.opts.Target, dialOpts...)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", s.opts.Target, err)
	}

	if err := waitReady(ctx, conn); err != nil {
		_ = conn.Close()

		return fmt.Errorf("connect to %s: %w", s.opts.Target, err)
	}

	s.conn = conn

	return nil
}

// waitReady blocks until the connection is usable or ctx runs out.
func waitReady(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()

	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}

		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

// Close releases the connection. Calling it twice is safe.
func (s *Sender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	if s.conn == nil {
		return nil
	}

	conn := s.conn
	s.conn = nil

	return conn.Close()
}

// Send performs one call. See engine.Sender for what the two failure channels
// mean: a returned error says the sender is unusable, while a non-success
// category inside the outcome is data about the target.
func (s *Sender) Send(ctx context.Context, req engine.Request) (engine.Outcome, error) {
	s.mu.RLock()
	conn, closed := s.conn, s.closed
	s.mu.RUnlock()

	switch {
	case closed:
		return engine.Outcome{}, ErrClosed
	case conn == nil:
		return engine.Outcome{}, ErrNotConnected
	}

	// A deadline already in the past is a timeout without touching the network:
	// the call had no budget left by the time it reached the sender.
	if !req.Deadline.IsZero() && !time.Now().Before(req.Deadline) {
		now := time.Now()

		return engine.Outcome{
			SentAt:   now,
			DoneAt:   now,
			Category: engine.CategoryTimeout,
			Code:     codes.DeadlineExceeded.String(),
			Err:      context.DeadlineExceeded,
		}, nil
	}

	call := &callStats{}
	callCtx := context.WithValue(ctx, callKey{}, call)

	if !req.Deadline.IsZero() {
		var cancel context.CancelFunc

		// Applied as given: recomputing it from now would hand a request that
		// queued for two seconds a fresh full budget.
		callCtx, cancel = context.WithDeadline(callCtx, req.Deadline)
		defer cancel()
	}

	payload := req.Payload
	body, target := responseTarget(req.KeepResponse)

	err := conn.Invoke(callCtx, req.Method, &payload, target)

	// The run was stopped: not a broken sender, but nothing was measured either.
	// gRPC does not wrap ctx.Err(), so the wrapping happens here — without it the
	// engine cannot tell a deliberate stop from a failure, and Ctrl+C would end
	// the run with a non-zero exit code.
	if err != nil && ctx.Err() != nil {
		return engine.Outcome{}, fmt.Errorf("call aborted with %s: %w", status.Code(err), ctx.Err())
	}

	outcome := engine.Outcome{
		SentAt:   call.sentAt,
		DoneAt:   call.doneAt,
		Category: categorize(err, call.answered),
		Code:     status.Code(err).String(),
		Err:      err,
	}
	if body != nil {
		outcome.Response = *body
	}

	return outcome, nil
}

// responseTarget picks what the codec decodes into: a byte slice when the run
// needs the body, and a sentinel that copies nothing when it does not.
func responseTarget(keep bool) (body *[]byte, decodeInto any) {
	if !keep {
		return nil, &discarded{}
	}

	body = new([]byte)

	return body, body
}
