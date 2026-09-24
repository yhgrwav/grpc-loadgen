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
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/test/bufconn"
)

// seeingTarget records the metadata and the client certificates of every call.
type seeingTarget struct {
	grpc_health_v1.UnimplementedHealthServer

	mu        sync.Mutex
	metadata  []metadata.MD
	peerCerts []int
}

func (s *seeingTarget) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	md, _ := metadata.FromIncomingContext(ctx)

	certs := 0
	if p, ok := peer.FromContext(ctx); ok {
		if info, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			certs = len(info.State.PeerCertificates)
		}
	}

	s.mu.Lock()
	s.metadata = append(s.metadata, md)
	s.peerCerts = append(s.peerCerts, certs)
	s.mu.Unlock()

	return &grpc_health_v1.HealthCheckResponse{}, nil
}

// listen serves target on a bufconn with the given server options and returns
// options for a sender dialing it.
func listen(t *testing.T, target grpc_health_v1.HealthServer, srvOpts ...grpc.ServerOption) Options {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(srvOpts...)
	grpc_health_v1.RegisterHealthServer(srv, target)

	go func() { _ = srv.Serve(lis) }()

	t.Cleanup(srv.Stop)

	return Options{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		})},
	}
}

func connected(t *testing.T, opts Options) *Sender {
	t.Helper()

	s := New(opts)
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	return s
}

// Ground: contract — Options.Metadata is the public API that carries auth to the target; every
// call carries every pair, over a plaintext connection too.
func TestMetadata_EveryCallCarriesIt(t *testing.T) {
	target := &seeingTarget{}
	opts := listen(t, target)
	opts.Metadata = map[string]string{"authorization": "Bearer abc", "x-api-key": "k"}
	sender := connected(t, opts)

	for range 3 {
		out, err := sender.Send(bounded(t), request(time.Now()))
		if err != nil || out.Code != "OK" {
			t.Fatalf("send: %v, code %v", err, out.Code)
		}
	}

	if len(target.metadata) != 3 {
		t.Fatalf("target saw %d calls, want 3", len(target.metadata))
	}
	for i, md := range target.metadata {
		for k, v := range opts.Metadata {
			if got := md.Get(k); len(got) != 1 || got[0] != v {
				t.Errorf("call %d: %s = %q, want [%q]", i, k, got, v)
			}
		}
	}
}

// Ground: contract — off means absent: without Options.Metadata nothing is added.
func TestMetadata_NoneWithoutTheOption(t *testing.T) {
	target := &seeingTarget{}
	sender := connected(t, listen(t, target))

	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := target.metadata[0].Get("authorization"); len(got) != 0 {
		t.Errorf("authorization = %q, want none", got)
	}
}

// Ground: contract — Options.RootCAs verifies a target whose certificate the system does not trust.
func TestTLS_OwnCAConnects(t *testing.T) {
	cert, pool := selfSigned(t)
	opts := listen(t, &seeingTarget{}, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	opts.TLS = true
	opts.RootCAs = pool

	sender := connected(t, opts)

	if out, err := sender.Send(bounded(t), request(time.Now())); err != nil || out.Code != "OK" {
		t.Fatalf("send: %v, code %v", err, out.Code)
	}
}

// Ground: contract — without the CA the same target fails Connect: the pool is not ignored.
func TestTLS_WithoutTheCAConnectFails(t *testing.T) {
	cert, _ := selfSigned(t)
	opts := listen(t, &seeingTarget{}, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	opts.TLS = true

	if err := New(opts).Connect(bounded(t)); err == nil {
		t.Fatal("connect succeeded against an untrusted certificate")
	}
}

// mutualServer requires a client certificate signed by clientCA.
func mutualServer(t *testing.T, target grpc_health_v1.HealthServer, clientCA *x509.CertPool, extra ...grpc.ServerOption) (Options, *x509.CertPool) {
	t.Helper()

	serverCert, serverPool := selfSigned(t)
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    clientCA,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	})

	opts := listen(t, target, append([]grpc.ServerOption{grpc.Creds(creds)}, extra...)...)
	opts.TLS = true
	opts.RootCAs = serverPool

	return opts, serverPool
}

// Ground: contract — Options.Certificates is presented to a target that asks for one.
func TestTLS_ClientCertificateReachesTheTarget(t *testing.T) {
	clientCert, clientPool := selfSigned(t)
	target := &seeingTarget{}
	opts, _ := mutualServer(t, target, clientPool)
	opts.Certificates = []tls.Certificate{clientCert}

	sender := connected(t, opts)

	if out, err := sender.Send(bounded(t), request(time.Now())); err != nil || out.Code != "OK" {
		t.Fatalf("send: %v, code %v", err, out.Code)
	}
	if target.peerCerts[0] != 1 {
		t.Errorf("target saw %d client certificates, want 1", target.peerCerts[0])
	}
}

// Ground: signal grpc-go v1.84.0 — internal/transport/http2_client.go:472 blocks the new
// transport until the server preface (its SETTINGS) arrives; a target that rejects the client
// certificate closes before sending it, so the connection never reaches READY and Connect fails.
// Under TLS 1.3 the client's own handshake has already succeeded by then: were grpc-go to mark
// the transport ready before the preface, the refusal would surface as a run of failed calls
// blamed on the target. 200 of 200 runs under -race on 2026-09-23.
func TestTLS_TargetRefusingTheClientFailsConnect(t *testing.T) {
	_, clientPool := selfSigned(t)
	opts, _ := mutualServer(t, &seeingTarget{}, clientPool)

	s := New(opts)
	t.Cleanup(func() { _ = s.Close() })

	err := s.Connect(bounded(t))
	if err == nil {
		t.Fatal("connect succeeded without the certificate the target requires")
	}
	if !errors.Is(err, ErrClosedAfterHandshake) {
		t.Errorf("err = %v, want ErrClosedAfterHandshake", err)
	}
}

// Ground: contract — metadata and a client certificate travel together.
func TestTLS_MetadataOverMutualTLS(t *testing.T) {
	clientCert, clientPool := selfSigned(t)
	target := &seeingTarget{}
	opts, _ := mutualServer(t, target, clientPool)
	opts.Certificates = []tls.Certificate{clientCert}
	opts.Metadata = map[string]string{"authorization": "Bearer abc"}

	sender := connected(t, opts)

	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := target.metadata[0].Get("authorization"); len(got) != 1 || got[0] != "Bearer abc" {
		t.Errorf("authorization = %q, want [Bearer abc]", got)
	}
}

// Ground: contract — Options.ServerName checks the target's certificate against a name, for a
// target reached by IP whose certificate names a host.
func TestTLS_ServerNameVerifiesATargetReachedByAnotherName(t *testing.T) {
	cert, pool := selfSigned(t) // valid for "bufnet" only
	opts := listen(t, &seeingTarget{}, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	opts.Target = "passthrough:///127.0.0.1:443"
	opts.TLS = true
	opts.RootCAs = pool

	if err := New(opts).Connect(bounded(t)); err == nil {
		t.Fatal("connect by IP succeeded against a certificate for bufnet")
	}

	opts.ServerName = "bufnet"
	sender := connected(t, opts)

	if out, err := sender.Send(bounded(t), request(time.Now())); err != nil || out.Code != "OK" {
		t.Fatalf("send: %v, code %v", err, out.Code)
	}
}

// Ground: contract — own roots replace the system pool rather than add to it: the TLS config
// holds exactly the pool given, and a nil pool means the system's.
func TestTLS_OwnCAReplacesTheSystemRoots(t *testing.T) {
	_, pool := selfSigned(t)

	if got := tlsConfig(Options{TLS: true, RootCAs: pool}).RootCAs; got != pool {
		t.Error("the TLS config does not hold exactly the given pool")
	}
	if got := tlsConfig(Options{TLS: true}).RootCAs; got != nil {
		t.Error("without own roots the TLS config must leave RootCAs nil: the system pool")
	}
}

// Ground: contract — verification is never switched off by any combination of options.
func TestTLS_VerificationIsNeverSkipped(t *testing.T) {
	cert, pool := selfSigned(t)

	for _, o := range []Options{
		{TLS: true},
		{TLS: true, RootCAs: pool},
		{TLS: true, ServerName: "bufnet"},
		{TLS: true, RootCAs: pool, Certificates: []tls.Certificate{cert}, ServerName: "bufnet"},
		{TLS: true, Metadata: map[string]string{"authorization": "x"}},
	} {
		if tlsConfig(o).InsecureSkipVerify {
			t.Errorf("InsecureSkipVerify on for %+v", o)
		}
	}
}

// Ground: contract — B1 reads the stream limit through the sender's own credentials; TLS and
// mTLS from Options must keep that wrapper, or the report loses its connection lines.
func TestConnections_ReadTheLimitUnderMutualTLS(t *testing.T) {
	clientCert, clientPool := selfSigned(t)
	opts, _ := mutualServer(t, &seeingTarget{}, clientPool, grpc.MaxConcurrentStreams(7))
	opts.Certificates = []tls.Certificate{clientCert}

	sender := connected(t, opts)
	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got, ok := sender.Connections(); !ok || !got.LimitAnnounced || got.FirstLimit != 7 {
		t.Errorf("connections %+v (known %v), want the limit 7 read under mTLS", got, ok)
	}
}

// Ground: contract — ServerName changes SNI and the name the certificate is checked against, and
// nothing else: :authority stays the target's address, so a target that routes by it sees the
// same request with or without the option.
func TestTLS_ServerNameLeavesTheAuthorityAlone(t *testing.T) {
	cert, pool := selfSigned(t)
	target := &authorityTarget{}
	opts := listen(t, target, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	opts.Target = "passthrough:///127.0.0.1:443"
	opts.TLS = true
	opts.RootCAs = pool
	opts.ServerName = "bufnet"

	sender := connected(t, opts)
	if _, err := sender.Send(bounded(t), request(time.Now())); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := target.seen.Load(); got == nil || *got != "127.0.0.1:443" {
		t.Errorf(":authority = %q, want the target's address 127.0.0.1:443", deref(got))
	}
}

type authorityTarget struct {
	grpc_health_v1.UnimplementedHealthServer

	seen atomic.Pointer[string]
}

func (a *authorityTarget) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (
	*grpc_health_v1.HealthCheckResponse, error,
) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(":authority"); len(v) == 1 {
		a.seen.Store(&v[0])
	}

	return &grpc_health_v1.HealthCheckResponse{}, nil
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}

	return *s
}
