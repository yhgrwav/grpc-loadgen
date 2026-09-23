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

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const secretToken = "Bearer s3cret-7f1c"

// pemPair is a self-signed certificate for 127.0.0.1 written as PEM files.
type pemPair struct {
	cert tls.Certificate
	pool *x509.CertPool
}

func writePair(t *testing.T, dir, name string) pemPair {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err = os.WriteFile(filepath.Join(dir, name+".pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, name+".key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	var p pemPair
	if p.cert, err = tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	p.pool = x509.NewCertPool()
	p.pool.AppendCertsFromPEM(certPEM)

	return p
}

// guardedTarget requires a client certificate and the token on every call,
// reflection included. authorized counts the unary calls that passed.
type guardedTarget struct {
	addr       string
	authorized atomic.Int64
	health     *health
}

func startGuardedTarget(t *testing.T, server, client pemPair) *guardedTarget {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	g := &guardedTarget{addr: lis.Addr().String(), health: &health{}}

	allowed := func(ctx context.Context) error {
		md, _ := metadata.FromIncomingContext(ctx)
		if got := md.Get("authorization"); len(got) != 1 || got[0] != secretToken {
			return status.Error(codes.Unauthenticated, "no token")
		}

		return nil
	}

	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{server.cert},
			ClientCAs:    client.pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if err := allowed(ctx); err != nil {
				return nil, err
			}
			g.authorized.Add(1)

			return h(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
			if err := allowed(ss.Context()); err != nil {
				return err
			}

			return h(srv, ss)
		}),
	)
	grpc_health_v1.RegisterHealthServer(srv, g.health)
	reflection.Register(srv)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return g
}

// guardedConfig writes a config reaching addr with the given app lines into
// dir; paths in them are relative to the config file.
func guardedConfig(t *testing.T, dir, addr, appLines, callLines string) string {
	t.Helper()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	cfg := "app:\n  target:\n    ip: " + host + "\n    port: " + port + "\n" + appLines +
		"load:\n  calls:\n    - method: " + checkMethod + "\n      rps: 50\n      duration: 300ms\n" + callLines

	path := filepath.Join(dir, "leettest.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

const guardedApp = "  ca: server.pem\n  cert: client.pem\n  key: client.key\n" +
	"  metadata:\n    authorization: ${LEETTEST_TEST_TOKEN}\n"

// Every call of a run against a target that demands a client certificate, its
// own CA and a token passes all three, and the report counts what arrived.
func TestRun_MutualTLSWithATokenReachesTheTarget(t *testing.T) {
	dir := t.TempDir()
	g := startGuardedTarget(t, writePair(t, dir, "server"), writePair(t, dir, "client"))
	t.Setenv("LEETTEST_TEST_TOKEN", secretToken)

	res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, guardedApp, ""))
	if res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}

	sent, failed := reportRow(t, res.stdout, checkMethod)
	if sent == 0 || failed != 0 {
		t.Fatalf("sent %d failed %d, want every call through", sent, failed)
	}
	if got := g.authorized.Load(); got != int64(sent) {
		t.Errorf("target authorized %d calls, report says %d sent", got, sent)
	}
}

// Reflection goes over the same connection and needs the same token: data
// is built from the schema only a guarded target can give.
func TestRun_ReflectionCarriesTheToken(t *testing.T) {
	dir := t.TempDir()
	g := startGuardedTarget(t, writePair(t, dir, "server"), writePair(t, dir, "client"))
	t.Setenv("LEETTEST_TEST_TOKEN", secretToken)

	cfg := guardedConfig(t, dir, g.addr, guardedApp, "      data:\n        service: wallet\n")
	if res := runCLI(t.Context(), t, 10*time.Second, "-c", cfg); res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}

	g.health.mu.Lock()
	got := g.health.service
	g.health.mu.Unlock()

	if got != "wallet" {
		t.Errorf("target received service = %q, want wallet", got)
	}
}

// The token is a secret: neither the report nor the progress repeats it.
func TestRun_TheTokenIsNeverPrinted(t *testing.T) {
	dir := t.TempDir()
	g := startGuardedTarget(t, writePair(t, dir, "server"), writePair(t, dir, "client"))
	t.Setenv("LEETTEST_TEST_TOKEN", secretToken)

	res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, guardedApp, ""))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}

	if strings.Contains(res.stdout+res.stderr, "s3cret") {
		t.Error("the token appears in the output")
	}
}

// A certificate file that cannot be read or is not PEM fails before any
// connection, naming the file.
func TestRun_BadCertificateFileFailsBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "garbage.pem"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePair(t, dir, "client")

	cases := []struct{ name, app, file string }{
		{"missing ca", "  ca: nowhere.pem\n", "nowhere.pem"},
		{"ca not pem", "  ca: garbage.pem\n", "garbage.pem"},
		{"cert not pem", "  cert: garbage.pem\n  key: client.key\n", "garbage.pem"},
		{"missing key", "  cert: client.pem\n  key: nowhere.key\n", "nowhere.key"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runCLI(t.Context(), t, 5*time.Second, "-c", guardedConfig(t, dir, closedPort(t), c.app, ""))
			if res.err == nil {
				t.Fatal("run succeeded")
			}
			if !strings.Contains(res.err.Error(), c.file) {
				t.Errorf("error %q does not name %s", res.err, c.file)
			}
			if strings.Contains(res.stderr, "connecting to") {
				t.Errorf("it tried to connect first:\n%s", res.stderr)
			}
		})
	}
}
