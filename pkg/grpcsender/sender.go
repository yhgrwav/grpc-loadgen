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
	"crypto/x509"
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

	"github.com/yhgrwav/leettest/pkg/engine"
)

// rawCall moves bytes through a call untouched. It is set per call, not on the
// connection: reflection shares the connection and needs the proto codec. A
// ready slice passed with ... costs no allocation per call, a fresh variadic one
// escapes to the heap.
var rawCall = []grpc.CallOption{grpc.ForceCodec(rawCodec{})}

var (
	ErrNotConnected = errors.New("sender is not connected, call Connect before the run")
	ErrClosed       = errors.New("sender is closed")
	// ErrClosedAfterHandshake is a target that completed the TLS handshake and
	// closed before its first SETTINGS: under TLS 1.3 that is how a refused
	// client certificate looks from the client.
	ErrClosedAfterHandshake = errors.New("the target closed the connection right after the TLS handshake; " +
		"it may require a client certificate or refuse the one given")
)

// Options describes the target and how to reach it.
type Options struct {
	// Target is the address to dial, as host:port.
	Target string
	// TLS turns on transport credentials; without it the connection is
	// insecure, which is the usual case for a service behind a mesh.
	TLS bool
	// RootCAs verifies the target under TLS; nil means the system pool.
	RootCAs *x509.CertPool
	// Certificates are presented to a target that asks for a client
	// certificate (mutual TLS). Used only with TLS.
	Certificates []tls.Certificate
	// ServerName is checked against the target's certificate and sent as SNI
	// instead of the host in Target; :authority stays Target's. Used only with
	// TLS.
	ServerName string
	// Metadata is sent with every call, such as authorization or x-api-key.
	// Keys must already be lowercase; nil adds nothing to a call.
	Metadata map[string]string
	// DialOptions are passed through for cases the fields above do not cover,
	// such as custom credentials or an in-process dialer in tests. Custom
	// transport credentials replace the sender's own, which read the stream
	// limit the target announces; Connections then reports nothing.
	DialOptions []grpc.DialOption
}

// Sender delivers calls to a real gRPC target.
type Sender struct {
	opts Options

	mu     sync.RWMutex
	conn   *grpc.ClientConn
	closed bool

	tracker *connTracker
	ready   readyWindow
	// streams tells a wait for a stream from a delay on our side.
	streams *streamGauge
	// stopWatch ends the connection watcher; watched closes when it has.
	stopWatch context.CancelFunc
	watched   chan struct{}
}

// New prepares a sender. It does not dial: the connection is established by
// Connect, before the run starts, so a wrong address fails immediately rather
// than a minute into the load.
func New(opts Options) *Sender {
	tracker := &connTracker{}

	return &Sender{opts: opts, tracker: tracker, streams: &streamGauge{limit: tracker.limit}}
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
		creds = credentials.NewTLS(tlsConfig(s.opts))
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(trackingCreds{TransportCredentials: creds, tracker: s.tracker, serverName: s.opts.ServerName}),
		grpc.WithStatsHandler(handler{streams: s.streams}),
		// A retry by a service config policy may follow an attempt the target
		// served, and would count one call for several. Transparent retries
		// stay: they follow only attempts the target never processed.
		grpc.WithDisableRetry(),
	}
	if len(s.opts.Metadata) > 0 {
		// Per-RPC credentials rather than a context per call: gRPC attaches
		// them itself, and a run without metadata has nothing in the send path.
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(staticMetadata(s.opts.Metadata)))
	}
	dialOpts = append(dialOpts, s.opts.DialOptions...)

	conn, err := grpc.NewClient(s.opts.Target, dialOpts...)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", s.opts.Target, err)
	}

	if err := waitReady(ctx, conn); err != nil {
		_ = conn.Close()

		if s.opts.TLS && s.tracker.handshookSilently() {
			return fmt.Errorf("connect to %s: %w: %w", s.opts.Target, ErrClosedAfterHandshake, err)
		}

		return fmt.Errorf("connect to %s: %w", s.opts.Target, err)
	}

	s.conn = conn

	// Recorded here, not by the watcher: the run starts the moment Connect
	// returns, before the watcher's goroutine may have run.
	s.ready.entered(time.Now())

	watchCtx, stop := context.WithCancel(context.Background())
	s.stopWatch, s.watched = stop, make(chan struct{})

	go s.watch(watchCtx, conn, s.watched)

	return nil
}

// watch records the connection's changes of state from READY, which Connect
// has already recorded, until ctx ends or the connection shuts down.
func (s *Sender) watch(ctx context.Context, conn *grpc.ClientConn, done chan<- struct{}) {
	defer close(done)

	for state := connectivity.Ready; conn.WaitForStateChange(ctx, state); {
		state = conn.GetState()

		switch state {
		case connectivity.Shutdown:
			return
		case connectivity.Ready:
			s.ready.entered(time.Now())
		default:
			s.ready.left(time.Now())
		}
	}
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
const probeMethod = "/leettest.v0.Probe/DoesNotExist"

// transportCause asks the connection why it failed, or returns nil if it works
// after all. grpc-go has no public accessor for the last connection error, but
// a call that does not wait for readiness fails at once with that error's text.
func transportCause(ctx context.Context, conn *grpc.ClientConn) error {
	empty := []byte{}
	probe := &callStats{}

	err := conn.Invoke(context.WithValue(ctx, callKey{}, probe), probeMethod, &empty, &discarded{}, rawCall...)
	if err == nil || probe.read().answered {
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

	err := conn.Close()

	s.stopWatch()
	<-s.watched

	return err
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
			NotSent:  true,
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

	err := conn.Invoke(callCtx, req.Method, &payload, target, rawCall...)

	// The run was stopped: not a broken sender, but nothing was measured either.
	// gRPC does not wrap ctx.Err(), so the wrapping happens here — without it the
	// engine cannot tell a deliberate stop from a failure, and Ctrl+C would end
	// the run with a non-zero exit code.
	if err != nil && ctx.Err() != nil {
		return engine.Outcome{}, fmt.Errorf("call aborted with %s: %w", status.Code(err), ctx.Err())
	}

	times := call.read()
	category := categorize(err, times.answered)
	sentAt, doneAt, notSent := timestamps(times, category)

	outcome := engine.Outcome{
		SentAt:     sentAt,
		NotSent:    notSent,
		StreamWait: times.streamWait(),
		DoneAt:     doneAt,
		Category:   category,
		Code:       status.Code(err).String(),
		Err:        err,
	}
	if notSent {
		outcome.NotSentOn = s.blocker(conn, times)
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

// Conn is the connection calls go through, for resolving method schemas over
// it before the run. Nil before Connect.
func (s *Sender) Conn() grpc.ClientConnInterface {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.conn
}

// timestamps picks the outcome's SentAt and DoneAt. A timeout whose body never
// went out gets a SentAt anyway: left zero, the pool would fall back to the
// moment Send was called and book the whole wait as the target's service time.
// Without headers the stream was never opened and the deadline expired waiting
// for stream quota, so nothing counts as service; with headers the target saw
// the stream and the rest is its own doing, such as a closed flow-control
// window.
func timestamps(call callTimes, category engine.Category) (sentAt, doneAt time.Time, notSent bool) {
	sentAt, doneAt = call.sentAt, call.doneAt
	if category != engine.CategoryTimeout || !sentAt.IsZero() {
		return sentAt, doneAt, false
	}

	if doneAt.IsZero() {
		doneAt = time.Now()
	}
	if !call.headerAt.IsZero() {
		return call.headerAt, doneAt, false
	}

	return doneAt, doneAt, true
}

// blocker says what an unsent call waited for. A connection not ready for all
// of the call, or not ready now in case the watcher is behind, is the
// connection. On a ready one it is a stream only if the connection was full at
// some moment of the wait; otherwise the delay was ours.
func (s *Sender) blocker(conn *grpc.ClientConn, t callTimes) engine.Blocker {
	switch {
	case t.begunAt.IsZero() || !s.ready.throughout(t.begunAt) || conn.GetState() != connectivity.Ready:
		return engine.BlockedOnConnection
	case s.streams.fullSince(t.waitFrom()):
		return engine.BlockedOnStream
	default:
		return engine.BlockedOnGenerator
	}
}

// staticMetadata is the same metadata for every call.
type staticMetadata map[string]string

func (m staticMetadata) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return m, nil
}

// RequireTransportSecurity is false: a plaintext target behind a mesh takes
// tokens too, and refusing them would be a rule the user has to learn.
func (staticMetadata) RequireTransportSecurity() bool { return false }

// tlsConfig is the one place TLS is configured. Verification stays on: no
// option here turns InsecureSkipVerify on, and a nil RootCAs is the system
// pool while a given one replaces it.
func tlsConfig(opts Options) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      opts.RootCAs,
		Certificates: opts.Certificates,
	}
}
