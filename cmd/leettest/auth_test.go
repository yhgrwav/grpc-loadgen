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
	"errors"
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

	"github.com/yhgrwav/leettest/pkg/config"
)

// secretToken is unique enough that finding it anywhere in the output can
// only mean it leaked.
const secretToken = "Bearer s3cret-7f1c9e0a"

const secretMark = "s3cret-7f1c9e0a"

// pemPair is a self-signed certificate written as name.pem and name.key.
type pemPair struct {
	cert tls.Certificate
	pool *x509.CertPool
}

type certSpec struct {
	ips      []net.IP
	dns      []string
	notAfter time.Time
}

func localCert() certSpec {
	return certSpec{ips: []net.IP{net.IPv4(127, 0, 0, 1)}, notAfter: time.Now().Add(time.Hour)}
}

func writePair(t *testing.T, dir, name string, spec certSpec) pemPair {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		IPAddresses: spec.ips, DNSNames: spec.dns,
		NotBefore: spec.notAfter.Add(-2 * time.Hour), NotAfter: spec.notAfter,
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

// guardedTarget serves TLS with server's certificate, requires a client
// certificate from clientCA when it is not nil, and the token on every call,
// reflection included. It announces a limit of 7 streams.
type guardedTarget struct {
	addr       string
	authorized atomic.Int64
	health     *health
}

func startGuardedTarget(t *testing.T, server pemPair, clientCA *x509.CertPool) *guardedTarget {
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

	cfg := &tls.Config{Certificates: []tls.Certificate{server.cert}, MinVersion: tls.VersionTLS12}
	if clientCA != nil {
		cfg.ClientCAs, cfg.ClientAuth = clientCA, tls.RequireAndVerifyClientCert
	}

	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(cfg)),
		grpc.MaxConcurrentStreams(7),
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

// mutual starts a target requiring the client certificate written next to it.
func mutual(t *testing.T) (dir string, g *guardedTarget) {
	t.Helper()

	dir = t.TempDir()
	server := writePair(t, dir, "server", localCert())
	client := writePair(t, dir, "client", localCert())
	t.Setenv("LEETTEST_TEST_TOKEN", secretToken)

	return dir, startGuardedTarget(t, server, client.pool)
}

// Every call of a run against a target that demands a client certificate, its
// own CA and a token passes all three, and the report counts what arrived.
func TestRun_MutualTLSWithATokenReachesTheTarget(t *testing.T) {
	dir, g := mutual(t)

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

// B1 still reads the stream limit when TLS and the client certificate come
// from the config: the report keeps its connection line.
func TestRun_MutualTLSKeepsTheStreamLimitLine(t *testing.T) {
	dir, g := mutual(t)

	res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, guardedApp, ""))
	if res.err != nil {
		t.Fatalf("run: %v", res.err)
	}
	if !strings.Contains(res.stdout, "target stream limit 7") {
		t.Errorf("no stream limit line under mTLS:\n%s", res.stdout)
	}
}

// Reflection goes over the same connection and needs the same token.
func TestRun_ReflectionCarriesTheToken(t *testing.T) {
	dir, g := mutual(t)

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

// The token never appears in stdout, stderr or the error, whether the run
// succeeds, the config is refused, or the target refuses the token.
func TestRun_TheTokenIsNeverPrinted(t *testing.T) {
	dir, g := mutual(t)

	scenarios := map[string]string{
		"success": guardedApp,
		// The token sits in a config refused for another reason.
		"config error": "  ca: server.pem\n  cert: client.pem\n  key: client.key\n" +
			"  metadata:\n    authorization: ${LEETTEST_TEST_TOKEN}\n    grpc-bad: " + secretToken + "\n",
		// A token the target does not accept: every call fails, the report names why.
		"target refuses": "  ca: server.pem\n  cert: client.pem\n  key: client.key\n" +
			"  metadata:\n    authorization: " + secretToken + "-wrong\n",
		// The value itself is invalid: the config error must not quote it.
		"bad value": "  ca: server.pem\n  cert: client.pem\n  key: client.key\n" +
			"  metadata:\n    authorization: \"" + secretToken + "\\u00e9\"\n",
	}

	for name, app := range scenarios {
		t.Run(name, func(t *testing.T) {
			res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, app, ""))

			if name == "success" && res.err != nil {
				t.Fatalf("run: %v", res.err)
			}

			all := res.stdout + res.stderr
			if res.err != nil {
				all += res.err.Error()
			}
			if strings.Contains(all, secretMark) {
				t.Errorf("the token appears in the output:\n%s", all)
			}
		})
	}
}

// Everything that can be known before the run fails before it: exit code 1
// (the run never started), nothing sent, and a message that names the cause.
func TestRun_TLSProblemsFailBeforeTheRun(t *testing.T) {
	dir := t.TempDir()
	good := writePair(t, dir, "server", localCert())
	client := writePair(t, dir, "client", localCert())
	writePair(t, dir, "other", localCert())
	expired := writePair(t, dir, "expired", certSpec{ips: localCert().ips, notAfter: time.Now().Add(-time.Hour)})
	named := writePair(t, dir, "named", certSpec{dns: []string{"api.internal"}, notAfter: time.Now().Add(time.Hour)})

	cases := []struct {
		name   string
		server pemPair
		mtls   bool
		app    string
		says   []string
	}{
		{"cert does not match key", good, false, "  ca: server.pem\n  cert: client.pem\n  key: other.key\n",
			[]string{"client.pem", "other.key"}},
		{"ca file missing", good, false, "  ca: nowhere.pem\n", []string{"nowhere.pem"}},
		{"key file missing", good, false, "  ca: server.pem\n  cert: client.pem\n  key: nowhere.key\n", []string{"nowhere.key"}},
		{"target certificate expired", expired, false, "  ca: expired.pem\n", []string{"expired"}},
		{"name does not match the address", named, false, "  ca: named.pem\n", []string{"127.0.0.1", "server_name"}},
		{"target refuses the client certificate", good, true, "  ca: server.pem\n", []string{"certificate"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var clientCA *x509.CertPool
			if c.mtls {
				clientCA = client.pool
			}
			g := startGuardedTarget(t, c.server, clientCA)

			res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, c.app, ""))
			if res.err == nil {
				t.Fatal("run succeeded")
			}
			if code := exitCode(res.err); code != 1 {
				t.Errorf("exit code %d, want 1: the run never started", code)
			}
			if strings.Contains(res.err.Error(), "unknown field") {
				t.Fatalf("refused as an unknown field, not for its cause: %v", res.err)
			}
			for _, want := range c.says {
				if !strings.Contains(res.err.Error(), want) {
					t.Errorf("error %q does not say %q", res.err, want)
				}
			}
			if strings.Contains(res.stderr, "running against") || res.stdout != "" {
				t.Errorf("a run started:\nstdout:\n%s\nstderr:\n%s", res.stdout, res.stderr)
			}
			if n := g.health.calls.Load(); n != 0 {
				t.Errorf("target received %d calls", n)
			}
		})
	}
}

// server_name lets a target reached by IP present a certificate for a host.
func TestRun_ServerNameReachesATargetByIP(t *testing.T) {
	dir := t.TempDir()
	named := writePair(t, dir, "named", certSpec{dns: []string{"api.internal"}, notAfter: time.Now().Add(time.Hour)})
	t.Setenv("LEETTEST_TEST_TOKEN", secretToken)
	g := startGuardedTarget(t, named, nil)

	app := "  ca: named.pem\n  server_name: api.internal\n  metadata:\n    authorization: ${LEETTEST_TEST_TOKEN}\n"
	res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, app, ""))
	if res.err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", res.err, res.stderr)
	}
	if g.authorized.Load() == 0 {
		t.Error("no call reached the target")
	}
}

// An invalid header is a config error before connecting, exit code 1, not a
// run where every call fails locally.
func TestRun_InvalidMetadataFailsBeforeConnecting(t *testing.T) {
	dir := t.TempDir()

	for name, line := range map[string]string{
		"reserved prefix": "grpc-timeout: 1S",
		"pseudo-header":   "\":path\": /x",
		"binary suffix":   "x-trace-bin: AAAA",
		"non-ASCII value": "x-user: \"caf\\u00e9\"",
	} {
		t.Run(name, func(t *testing.T) {
			res := runCLI(t.Context(), t, 5*time.Second, "-c",
				guardedConfig(t, dir, closedPort(t), "  tls: false\n  metadata:\n    "+line+"\n", ""))
			if res.err == nil {
				t.Fatal("run succeeded")
			}
			if code := exitCode(res.err); code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if !errors.Is(res.err, config.ErrInvalidMetadata) {
				t.Errorf("err = %v, want ErrInvalidMetadata", res.err)
			}
			if strings.Contains(res.stderr, "connecting to") {
				t.Errorf("it tried to connect first:\n%s", res.stderr)
			}
		})
	}
}

// The methods are checked before the run with the same metadata. A target
// that refuses the token there stops the run before it starts, saying the
// credentials were rejected, never quoting them.
func TestRun_CredentialsRejectedAtTheMethodCheckStopBeforeTheRun(t *testing.T) {
	dir, g := mutual(t)

	app := "  ca: server.pem\n  cert: client.pem\n  key: client.key\n" +
		"  metadata:\n    authorization: " + secretToken + "-wrong\n"
	res := runCLI(t.Context(), t, 10*time.Second, "-c", guardedConfig(t, dir, g.addr, app, ""))

	if res.err == nil {
		t.Fatal("run succeeded with a token the target refuses")
	}
	if code := exitCode(res.err); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if !strings.Contains(res.err.Error(), "target rejected credentials") {
		t.Errorf("error %q does not say the credentials were rejected", res.err)
	}
	if strings.Contains(res.stdout+res.stderr+res.err.Error(), secretMark) {
		t.Error("the token appears in the output")
	}
	if n := g.health.calls.Load(); n != 0 {
		t.Errorf("target received %d calls of the run", n)
	}
}

// TLS on against a plaintext target, and off against a TLS one: the two
// commonest setup mistakes. Each fails before the run with exit code 1, a
// message naming app.tls, and within the connect timeout — without TLS the
// client waits for a server preface that never comes (grpc-go v1.84.0,
// http2_client.go:472).
func TestRun_TLSMismatchFailsWithinTheConnectTimeout(t *testing.T) {
	dir := t.TempDir()
	server := writePair(t, dir, "server", localCert())
	tlsTarget := startGuardedTarget(t, server, nil)
	plainTarget := startTarget(t)

	cases := []struct{ name, addr, app string }{
		{"TLS on, plaintext target", plainTarget.addr, "  tls: true\n"},
		{"TLS off, TLS target", tlsTarget.addr, "  tls: false\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			res := runCLI(t.Context(), t, 8*time.Second, "-connect-timeout", "2s",
				"-c", guardedConfig(t, dir, c.addr, c.app, ""))

			if res.err == nil {
				t.Fatal("run succeeded")
			}
			if took := time.Since(start); took > 3*time.Second {
				t.Errorf("failed after %v; the connect timeout is 2s", took)
			}
			if code := exitCode(res.err); code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if !strings.Contains(res.err.Error(), "app.tls") {
				t.Errorf("error %q does not point at app.tls", res.err)
			}
		})
	}
}

// A password-protected key cannot be read; the error says that, not the
// parser's own words.
func TestRun_EncryptedKeyIsRefusedByName(t *testing.T) {
	dir := t.TempDir()
	writePair(t, dir, "client", localCert())

	for name, block := range map[string]*pem.Block{
		"PKCS#8 encrypted": {Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{0x30, 0x00}},
		"legacy Proc-Type": {Type: "EC PRIVATE KEY", Headers: map[string]string{
			"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,00000000000000000000000000000000",
		}, Bytes: []byte{0x00}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "locked.key"), pem.EncodeToMemory(block), 0o600); err != nil {
				t.Fatal(err)
			}

			res := runCLI(t.Context(), t, 5*time.Second, "-c",
				guardedConfig(t, dir, closedPort(t), "  cert: client.pem\n  key: locked.key\n", ""))
			if res.err == nil || !strings.Contains(res.err.Error(), "encrypted keys are not supported") {
				t.Errorf("err = %v, want \"encrypted keys are not supported\"", res.err)
			}
			if code := exitCode(res.err); code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
		})
	}
}

// app.ca replaces the system roots: the pool holds the file's certificates
// and nothing else.
func TestSenderOptions_OwnCAReplacesTheSystemRoots(t *testing.T) {
	dir := t.TempDir()
	server := writePair(t, dir, "server", localCert())

	opts, err := senderOptions(&config.App{UseTLS: true, CA: filepath.Join(dir, "server.pem")})
	if err != nil {
		t.Fatal(err)
	}

	if opts.RootCAs == nil || !opts.RootCAs.Equal(server.pool) {
		t.Error("the root pool is not exactly the certificates of app.ca")
	}
}
