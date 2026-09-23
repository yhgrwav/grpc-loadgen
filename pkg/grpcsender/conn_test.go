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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// frame builds an HTTP/2 frame header and payload: RFC 9113 §4.1.
func frame(kind byte, payload []byte) []byte {
	out := []byte{byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload)), kind, 0, 0, 0, 0, 0}

	return append(out, payload...)
}

// settings builds a SETTINGS payload of id/value pairs: RFC 9113 §6.5.1.
func settings(pairs ...uint32) []byte {
	var out []byte
	for i := 0; i+1 < len(pairs); i += 2 {
		out = binary.BigEndian.AppendUint16(out, uint16(pairs[i]))
		out = binary.BigEndian.AppendUint32(out, pairs[i+1])
	}

	return out
}

const (
	frameData     = 0x0
	frameSettings = 0x4

	settingInitialWindow = 0x4
)

// Ground: boundary — the reads that cut the first frame depend on the network and TLS record
// sizes; an end-to-end test cannot make a read stop at a given byte.
func TestSettingsReader_FindsTheLimitHoweverTheReadsCutTheFrame(t *testing.T) {
	tests := []struct {
		name      string
		bytes     []byte
		limit     uint32
		announced bool
	}{
		{"limit alone", frame(frameSettings, settings(settingMaxStreams, 1)), 1, true},
		{"limit among others", frame(frameSettings, settings(settingInitialWindow, 65535, settingMaxStreams, 250)), 250, true},
		// Zero is a limit: no new streams at all. It is not "no limit".
		{"limit zero", frame(frameSettings, settings(settingMaxStreams, 0)), 0, true},
		// RFC 9113 §6.5.3: values are processed in order, the last one stands.
		{"limit twice, the last stands", frame(frameSettings, settings(settingMaxStreams, 5, settingMaxStreams, 7)), 7, true},
		{"no limit", frame(frameSettings, settings(settingInitialWindow, 65535)), 0, false},
		{"empty settings", frame(frameSettings, nil), 0, false},
		// Not what the protocol allows; whatever it is, it announced nothing.
		{"first frame is not settings", frame(frameData, settings(settingMaxStreams, 1)), 0, false},
	}

	for _, tt := range tests {
		for _, cut := range []int{0, 1, 5} {
			t.Run(fmt.Sprintf("%s, cut %d", tt.name, cut), func(t *testing.T) {
				// A second frame right behind the first: it must not be read as part of it.
				stream := append(append([]byte{}, tt.bytes...), frame(frameSettings, settings(settingMaxStreams, 99))...)

				var r settingsReader

				done, fed := false, 0
				for fed < len(stream) && !done {
					n := len(stream) - fed
					if cut > 0 {
						n = min(n, cut)
					}
					done = r.feed(stream[fed : fed+n])
					fed += n
				}

				if !done {
					t.Fatalf("the first frame was fed whole and the reader still waits")
				}
				if r.limit != tt.limit || r.announced != tt.announced {
					t.Errorf("limit %d announced %v, want %d %v", r.limit, r.announced, tt.limit, tt.announced)
				}
			})
		}
	}
}

// Ground: boundary — the cases are a hundred milliseconds apart on the connection's state; an
// end-to-end test cannot line a call up with a reconnect that precisely.
func TestReadyWindow_ReadyOnlyWhenTheWholeCallWasReady(t *testing.T) {
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }

	tests := []struct {
		name    string
		events  func(w *readyWindow)
		begun   time.Time
		through bool
	}{
		{"never seen ready", func(*readyWindow) {}, at(1050), false},
		{"ready since before the call", func(w *readyWindow) { w.entered(at(500)) }, at(1050), true},
		{"ready at the very moment the call began", func(w *readyWindow) { w.entered(at(1050)) }, at(1050), true},
		// The reviewer's case: out at 1.00, back at 1.10, the call began at 1.05.
		{"back to ready after the call began", func(w *readyWindow) {
			w.entered(at(500))
			w.left(at(1000))
			w.entered(at(1100))
		}, at(1050), false},
		{"left during the call and not back", func(w *readyWindow) {
			w.entered(at(500))
			w.left(at(1100))
		}, at(1050), false},
		{"left and back before the call", func(w *readyWindow) {
			w.entered(at(500))
			w.left(at(800))
			w.entered(at(900))
		}, at(1050), true},
		// Left during the call and back again: an interruption, not ready throughout.
		{"a blip inside the call", func(w *readyWindow) {
			w.entered(at(500))
			w.left(at(1100))
			w.entered(at(1120))
		}, at(1050), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w readyWindow

			tt.events(&w)

			if got := w.throughout(tt.begun); got != tt.through {
				t.Errorf("ready throughout = %v, want %v", got, tt.through)
			}
		})
	}
}

// Ground: concurrency — the watcher writes the window from its own goroutine while every
// unsent call reads it; -race catches an unguarded field.
func TestReadyWindow_ReadWhileTheWatcherWrites(t *testing.T) {
	var w readyWindow

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := range 1000 {
			w.entered(time.Unix(int64(i), 0))
			w.left(time.Unix(int64(i), 1))
		}
	}()

	for range 1000 {
		_ = w.throughout(time.Unix(500, 0))
	}

	<-done
}

// Ground: boundary — an unsent call behind a busy stream on a ready connection must be put down
// to the stream; the sender is the only one who can tell it from a connection wait.
func TestSend_UnsentBehindABusyStreamIsBlockedOnTheStream(t *testing.T) {
	sender := oneStreamTarget(t)

	req := request(time.Now())
	req.Deadline = req.ScheduledAt.Add(100 * time.Millisecond)

	out, err := sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if !out.NotSent {
		t.Fatalf("not sent = false: the only stream was held the whole time")
	}
	if out.NotSentOn != engine.BlockedOnStream {
		t.Errorf("blocked on %v, want the stream: the connection was ready throughout", out.NotSentOn)
	}
}

// Ground: boundary — the stream wait of a call that went out is the gap between the stream
// being freed and the call's own start; only the sender sees both.
func TestSend_ACallThatWaitedForAStreamCarriesTheWait(t *testing.T) {
	const held = 80 * time.Millisecond

	hold := &holdingTarget{entered: make(chan struct{}, 2), release: make(chan struct{})}
	sender := serve(t, hold, grpc.MaxConcurrentStreams(1))

	first := make(chan struct{})

	go func() {
		defer close(first)
		_, _ = sender.Send(context.Background(), request(time.Now()))
	}()

	select {
	case <-hold.entered:
	case <-bounded(t).Done():
		t.Fatal("the holding call never reached the target")
	}

	// Real time is the thing measured here: the second call must wait on the
	// stream for about this long.
	start := time.Now()

	go func() {
		time.Sleep(held)
		close(hold.release)
	}()

	out, err := sender.Send(bounded(t), request(start))
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	<-first

	if out.NotSent {
		t.Fatalf("not sent: the stream was freed well before any deadline")
	}
	if out.StreamWait < held-5*time.Millisecond {
		t.Errorf("stream wait %v, the stream was busy for %v after the call began", out.StreamWait, held)
	}
	if limit := out.DoneAt.Sub(start); out.StreamWait > limit {
		t.Errorf("stream wait %v is longer than the whole call, %v", out.StreamWait, limit)
	}
}

// Ground: boundary — a call that went out at once must carry no wait, or every call of an
// unloaded run would count as waited. The target answers in 20ms so that a wait measured to the
// end of the call, not to its headers, goes red.
func TestSend_ACallWithAFreeStreamDidNotWait(t *testing.T) {
	sender := serve(t, slowTarget{delay: 20 * time.Millisecond})

	out, err := sender.Send(bounded(t), request(time.Now()))
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if out.StreamWait > engine.StreamWaitFloor {
		t.Errorf("stream wait %v on an idle connection, want under %v", out.StreamWait, engine.StreamWaitFloor)
	}
}

// Ground: boundary — a call that expires while the connection is being re-established must be
// put down to the connection, never the stream; the stand cannot fix the moment of a reconnect.
// Mutation "every unsent call is a stream wait" must turn this red.
func TestSend_UnsentWhileReconnectingIsBlockedOnTheConnection(t *testing.T) {
	target := dropping(t)

	target.drop()

	req := request(time.Now())
	req.Deadline = req.ScheduledAt.Add(100 * time.Millisecond)

	out, err := target.sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if !out.NotSent {
		t.Fatalf("not sent = false, category %v: the connection was down the whole time", out.Category)
	}
	if out.NotSentOn != engine.BlockedOnConnection {
		t.Errorf("blocked on %v, want the connection", out.NotSentOn)
	}

	target.open()

	if _, err := target.sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send after the reconnect: %v", err)
	}

	if got, _ := target.sender.Connections(); got.Reconnects != 1 {
		t.Errorf("reconnects %d, want 1: one handshake after the first", got.Reconnects)
	}
}

// Ground: contract — the report names the limit and whose it is; grpcsender.Sender is exported.
func TestConnections_NameTheTargetsLimit(t *testing.T) {
	tests := []struct {
		name string
		opts []grpc.ServerOption
		want engine.Connections
	}{
		{"limit one", []grpc.ServerOption{grpc.MaxConcurrentStreams(1)},
			engine.Connections{Open: 1, LimitAnnounced: true, FirstLimit: 1, LastLimit: 1}},
		// grpc-go's server does not send the setting unless asked to.
		{"no limit", nil, engine.Connections{Open: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := serve(t, &holdingTarget{entered: make(chan struct{}, 1), release: closed()}, tt.opts...)

			if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
				t.Fatalf("send: %v", err)
			}

			if got, ok := sender.Connections(); !ok || got != tt.want {
				t.Errorf("connections %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Ground: signal grpc-go v1.84.0 — with no MAX_CONCURRENT_STREAMS in the target's first
// SETTINGS the client sets its quota to MaxUint32 (internal/transport/http2_client.go:1344);
// 100 is only its value before SETTINGS arrives. If an upgrade brings a client limit back, the
// report's "no limit announced" would hide a limit of our own.
func TestGRPC_NoAnnouncedLimitMeansNoClientLimit(t *testing.T) {
	const calls = 300

	var inside, peak atomic.Int32

	gate := make(chan struct{})
	target := &countingTarget{inside: &inside, peak: &peak, gate: gate, want: calls}
	sender := serve(t, target)

	var wg sync.WaitGroup
	for range calls {
		wg.Add(1)

		go func() {
			defer wg.Done()
			_, _ = sender.Send(bounded(t), request(time.Now()))
		}()
	}

	select {
	case <-target.all():
	case <-bounded(t).Done():
		t.Errorf("only %d of %d calls reached the target at once", peak.Load(), calls)
	}

	close(gate)
	wg.Wait()
}

// --- helpers --------------------------------------------------------------

func closed() chan struct{} {
	c := make(chan struct{})
	close(c)

	return c
}

// serve starts a health server with opts on bufconn and connects a sender.
func serve(t *testing.T, target grpc_health_v1.HealthServer, opts ...grpc.ServerOption) *Sender {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(opts...)
	grpc_health_v1.RegisterHealthServer(srv, target)

	go func() { _ = srv.Serve(lis) }()

	sender := New(Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})
	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		_ = sender.Close()
		srv.Stop()
	})

	return sender
}

// droppingTarget is a server whose connection the test can cut and whose
// next dial it can hold, so a call meets a connection that is not ready.
type droppingTarget struct {
	sender *Sender

	mu   sync.Mutex
	conn net.Conn
	gate chan struct{}
}

func dropping(t *testing.T) *droppingTarget {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, &holdingTarget{entered: make(chan struct{}, 16), release: closed()})

	go func() { _ = srv.Serve(lis) }()

	d := &droppingTarget{gate: closed()}

	d.sender = New(Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				d.mu.Lock()
				gate := d.gate
				d.mu.Unlock()

				select {
				case <-gate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}

				conn, err := lis.DialContext(ctx)
				if err == nil {
					d.mu.Lock()
					d.conn = conn
					d.mu.Unlock()
				}

				return conn, err
			}),
		},
	})
	if err := d.sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		d.open()
		_ = d.sender.Close()
		srv.Stop()
	})

	return d
}

// drop cuts the connection and holds the next dial until open.
func (d *droppingTarget) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.gate = make(chan struct{})
	_ = d.conn.Close()
}

func (d *droppingTarget) open() {
	d.mu.Lock()
	defer d.mu.Unlock()

	select {
	case <-d.gate:
	default:
		close(d.gate)
	}
}

// countingTarget holds every call until gate closes and reports when want
// of them are inside at once.
type countingTarget struct {
	grpc_health_v1.UnimplementedHealthServer

	inside, peak *atomic.Int32
	gate         chan struct{}
	want         int32

	once    sync.Once
	reached chan struct{}
}

func (c *countingTarget) all() chan struct{} {
	c.once.Do(func() { c.reached = make(chan struct{}) })

	return c.reached
}

func (c *countingTarget) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	reached := c.all()

	n := c.inside.Add(1)
	for p := c.peak.Load(); n > p && !c.peak.CompareAndSwap(p, n); p = c.peak.Load() {
	}
	if n == c.want {
		close(reached)
	}

	select {
	case <-c.gate:
	case <-ctx.Done():
	}

	c.inside.Add(-1)

	return &grpc_health_v1.HealthCheckResponse{}, nil
}

// slowTarget answers every call after delay.
type slowTarget struct {
	grpc_health_v1.UnimplementedHealthServer

	delay time.Duration
}

func (s slowTarget) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}

	return &grpc_health_v1.HealthCheckResponse{}, nil
}

// Ground: boundary — the run starts the moment Connect returns; a call begun then must already
// count as on a ready connection, or an unsent one is put down to the connection that was fine.
func TestConnect_TheConnectionIsReadyFromTheMomentConnectReturns(t *testing.T) {
	for range 50 {
		sender := serve(t, slowTarget{})

		if begun := time.Now(); !sender.ready.throughout(begun) {
			t.Fatalf("a call begun right after Connect is not on a ready connection")
		}
	}
}

// selfSigned makes a certificate for the name bufconn targets resolve to.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bufnet"}, DNSNames: []string{"bufnet"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// Ground: contract — under TLS the frames are readable only after the handshake; a wrapper put
// on the raw connection reads ciphertext and finds no limit. This goes through Connect with
// TLS on, the path a user takes. Mutation "wrap before the TLS handshake" turns it red.
func TestConnections_ReadTheLimitUnderTLS(t *testing.T) {
	cert, pool := selfSigned(t)

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&cert)), grpc.MaxConcurrentStreams(7))
	grpc_health_v1.RegisterHealthServer(srv, slowTarget{})

	go func() { _ = srv.Serve(lis) }()

	sender := New(Options{
		Target: "passthrough:///bufnet",
		TLS:    true,
		DialOptions: []grpc.DialOption{grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		})},
	})
	sender.rootCAs = pool

	t.Cleanup(func() {
		_ = sender.Close()
		srv.Stop()
	})

	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect over TLS: %v", err)
	}
	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}

	want := engine.Connections{Open: 1, LimitAnnounced: true, FirstLimit: 7, LastLimit: 7}
	if got, ok := sender.Connections(); !ok || got != want {
		t.Errorf("connections %+v (known %v), want %+v", got, ok, want)
	}
}

// Ground: contract — credentials of the caller's in DialOptions replace the sender's, so no
// handshake passes its wrapper; the report must then say nothing about connections rather than
// "no limit announced" about a limit nobody read. Goes through the engine, as the CLI does.
// Mutation "report what was seen even with no handshake" turns it red.
func TestConnections_CallersOwnCredentialsLeaveTheReportSilent(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.MaxConcurrentStreams(1))
	grpc_health_v1.RegisterHealthServer(srv, slowTarget{})

	go func() { _ = srv.Serve(lis) }()

	sender := New(Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		},
	})

	t.Cleanup(func() {
		_ = sender.Close()
		srv.Stop()
	})

	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	eng, err := engine.New(engine.Options{
		Calls: []engine.Call{{
			Method: checkMethod, Timeout: time.Second,
			Stages: []engine.Stage{{StartRPS: 10, TargetRPS: 10, Duration: 200 * time.Millisecond}},
		}},
		Sender:      sender,
		MaxInFlight: 20,
	})
	if err != nil {
		t.Fatalf("build the engine: %v", err)
	}
	if err := eng.Run(bounded(t)); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := eng.Report().Connections; got != nil {
		t.Errorf("connections %+v, want none: no handshake went through the sender's credentials", *got)
	}
}

// Ground: boundary — with no limit announced the time from the pick to the headers is the
// scheduler's, not a stream wait: CI saw 1–3 calls over 1ms at 300 rps (run 35901535339). An
// end-to-end test sees it only on a loaded runner, at random.
func TestStreamWait_NoLimitAnnouncedNoStreamWait(t *testing.T) {
	begun := time.Now()
	times := callTimes{begunAt: begun, headerAt: begun.Add(5 * time.Millisecond)}

	if got := times.streamWait(false); got != 0 {
		t.Errorf("stream wait %v with no limit announced, want 0", got)
	}
	if got := times.streamWait(true); got != 5*time.Millisecond {
		t.Errorf("stream wait %v under a limit, want 5ms", got)
	}
}

// Ground: boundary — an unsent call on a ready connection with no limit announced cannot have
// waited for a stream; putting it there would print a stream verdict about a limit that does
// not exist.
func TestOnStream_NoLimitAnnouncedIsNeverAStreamWait(t *testing.T) {
	sender := serve(t, slowTarget{})

	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}

	if sender.onStream(sender.conn, time.Now()) {
		t.Errorf("a call on a ready connection with no limit announced was put down to streams")
	}
}
