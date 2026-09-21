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
	"strings"
	"sync"
	"sync/atomic"
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

	// paths maps a method as the config names it to its gRPC path. Copied on
	// write: a method is added once, and every request after that reads the
	// map without a lock or an allocation.
	paths   atomic.Pointer[map[string]string]
	pathsMu sync.Mutex
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
// The first failed attempt is an error: a refused connection, a name that does
// not resolve, a failed handshake. ctx only bounds a target that neither
// answers nor refuses.
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

// waitReady blocks until the connection is usable, the first attempt fails, or
// ctx runs out. Waiting past a failure would wait forever: grpc-go backs off and
// retries an unreachable target without end.
func waitReady(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()

	for {
		state := conn.GetState()

		switch state {
		case connectivity.Ready:
			return nil
		case connectivity.TransientFailure:
			if cause := transportCause(ctx, conn); cause != nil {
				return cause
			}

			continue
		case connectivity.Shutdown:
			return ErrClosed
		}

		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

// probeMethod is a path no service implements. The probe must not be able to
// do anything if it does reach a target.
const probeMethod = "/grpc.loadgen.v0.Probe/DoesNotExist"

// transportCause asks the connection why it failed, or returns nil if it works
// after all. grpc-go has no public accessor for the last connection error, but
// a call that does not wait for readiness fails at once with that error's text.
func transportCause(ctx context.Context, conn *grpc.ClientConn) error {
	empty := []byte{}
	probe := &callStats{}

	err := conn.Invoke(context.WithValue(ctx, callKey{}, probe), probeMethod, &empty, &discarded{})
	if err == nil || probe.answered {
		// The connection came up between the failure and the probe. Any status
		// from the target proves it, not only Unimplemented: a service that
		// checks credentials before routing says Unauthenticated instead.
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	return errors.New(status.Convert(err).Message())
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

	err := conn.Invoke(callCtx, s.path(req.Method), &payload, target)

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

// path turns the method a request names into the path gRPC sends. The config
// writes pkg.Service/Method; without the leading slash every call would come
// back Unimplemented.
func (s *Sender) path(method string) string {
	if strings.HasPrefix(method, "/") {
		return method
	}
	if paths := s.paths.Load(); paths != nil {
		if p, ok := (*paths)[method]; ok {
			return p
		}
	}

	s.pathsMu.Lock()
	defer s.pathsMu.Unlock()

	next := make(map[string]string)
	if paths := s.paths.Load(); paths != nil {
		for k, v := range *paths {
			next[k] = v
		}
	}
	p := "/" + method
	next[method] = p
	s.paths.Store(&next)

	return p
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
