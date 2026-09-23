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
	"net"
	"sync"
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
func mutualServer(t *testing.T, target grpc_health_v1.HealthServer, clientCA *x509.CertPool) (Options, *x509.CertPool) {
	t.Helper()

	serverCert, serverPool := selfSigned(t)
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    clientCA,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	})

	opts := listen(t, target, grpc.Creds(creds))
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

// Ground: contract — a target that refuses the client is an error before the run, not a run of
// failed calls blamed on the target. Under TLS 1.3 the client finishes its half of the handshake
// before the server checks the certificate, so the refusal arrives after the client thinks it is
// connected.
func TestTLS_TargetRefusingTheClientFailsConnect(t *testing.T) {
	_, clientPool := selfSigned(t)
	opts, _ := mutualServer(t, &seeingTarget{}, clientPool)

	s := New(opts)
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Connect(bounded(t)); err == nil {
		t.Fatal("connect succeeded without the certificate the target requires")
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
