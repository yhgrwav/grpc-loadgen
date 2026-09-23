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

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yhgrwav/leettest/pkg/config"
)

// withApp is validYAML with extra lines appended under app:.
func withApp(lines string) []byte {
	return []byte(strings.Replace(validYAML, "app:\n", "app:\n"+lines, 1))
}

// Ground: contract — pkg/config is a library API; callers get this without our CLI.
func TestMetadata_KeysAreLowercasedAndValuesKeptAsWritten(t *testing.T) {
	cfg, err := config.Parse(withApp("  metadata:\n    Authorization: Bearer abc\n    x-api-key: \"k 1\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := map[string]string{"authorization": "Bearer abc", "x-api-key": "k 1"}
	if len(cfg.App.Metadata) != len(want) {
		t.Fatalf("metadata %v, want %v", cfg.App.Metadata, want)
	}
	for k, v := range want {
		if cfg.App.Metadata[k] != v {
			t.Errorf("metadata[%q] = %q, want %q", k, cfg.App.Metadata[k], v)
		}
	}
}

// Ground: contract — off means absent: no metadata leaves the map nil, so the sender adds nothing.
func TestMetadata_AbsentIsNil(t *testing.T) {
	for _, raw := range [][]byte{[]byte(validYAML), withApp("  metadata: {}\n")} {
		cfg, err := config.Parse(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cfg.App.Metadata != nil {
			t.Errorf("metadata %v, want nil", cfg.App.Metadata)
		}
	}
}

// Ground: contract — a key gRPC reserves or HTTP/2 cannot carry fails before the run, naming the
// key; the value is not printed, it may be a secret.
func TestMetadata_BadKeysAreRefused(t *testing.T) {
	cases := []struct{ name, key string }{
		{"reserved grpc- prefix", "grpc-timeout"},
		{"pseudo-header", "\":authority\""},
		{"space inside", "\"x api\""},
		{"empty", "\"\""},
		{"same key twice after lowercasing", "X-Api-Key: a\n    x-api-key"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.Parse(withApp("  metadata:\n    " + c.key + ": s3cret\n"))
			if !errors.Is(err, config.ErrInvalidMetadata) {
				t.Fatalf("err = %v, want ErrInvalidMetadata", err)
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("the error prints the value: %v", err)
			}
		})
	}
}

// Ground: contract — secrets stay out of the file: ${NAME} is read from the environment.
func TestMetadata_ValueFromTheEnvironment(t *testing.T) {
	t.Setenv("LEETTEST_TOKEN", "t0ken")

	cfg, err := config.Parse(withApp("  metadata:\n    authorization: Bearer ${LEETTEST_TOKEN}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.App.Metadata["authorization"]; got != "Bearer t0ken" {
		t.Errorf("authorization = %q, want %q", got, "Bearer t0ken")
	}
}

// Ground: contract — an unset variable would send an empty token and every call would fail as
// the target's fault; it is refused before the run, naming the variable.
func TestMetadata_UnsetVariableIsRefused(t *testing.T) {
	_, err := config.Parse(withApp("  metadata:\n    authorization: Bearer ${LEETTEST_NOT_SET_ANYWHERE}\n"))
	if !errors.Is(err, config.ErrInvalidMetadata) {
		t.Fatalf("err = %v, want ErrInvalidMetadata", err)
	}
	if !strings.Contains(err.Error(), "LEETTEST_NOT_SET_ANYWHERE") {
		t.Errorf("the error does not name the variable: %v", err)
	}
}

// Ground: contract — only ${NAME} is a reference: a lone $ in a token is kept, not eaten.
func TestMetadata_ALoneDollarIsKept(t *testing.T) {
	cfg, err := config.Parse(withApp("  metadata:\n    x-api-key: a$b\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.App.Metadata["x-api-key"]; got != "a$b" {
		t.Errorf("x-api-key = %q, want %q", got, "a$b")
	}
}

// Ground: contract — certificate files with TLS off would be silently ignored.
func TestTLSFiles_RefusedWithTLSOff(t *testing.T) {
	for _, line := range []string{"  ca: ca.pem\n", "  cert: c.pem\n  key: k.pem\n"} {
		_, err := config.Parse(withApp("  tls: false\n" + line))
		if !errors.Is(err, config.ErrTLSFilesWithoutTLS) {
			t.Errorf("%q: err = %v, want ErrTLSFilesWithoutTLS", line, err)
		}
	}
}

// Ground: contract — a client certificate is useless without its key and the other way round.
func TestTLSFiles_CertAndKeyComeTogether(t *testing.T) {
	for _, line := range []string{"  cert: c.pem\n", "  key: k.pem\n"} {
		_, err := config.Parse(withApp(line))
		if !errors.Is(err, config.ErrCertWithoutKey) {
			t.Errorf("%q: err = %v, want ErrCertWithoutKey", line, err)
		}
	}
}

// Ground: contract — a relative path means next to the config file, wherever the run starts;
// an absolute one is kept.
func TestTLSFiles_RelativeToTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "elsewhere", "key.pem")
	path := filepath.Join(dir, "run.yaml")

	if err := os.WriteFile(path, withApp("  ca: certs/ca.pem\n  cert: client.pem\n  key: "+abs+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if want := filepath.Join(dir, "certs", "ca.pem"); cfg.App.CA != want {
		t.Errorf("ca = %q, want %q", cfg.App.CA, want)
	}
	if want := filepath.Join(dir, "client.pem"); cfg.App.Cert != want {
		t.Errorf("cert = %q, want %q", cfg.App.Cert, want)
	}
	if cfg.App.Key != abs {
		t.Errorf("key = %q, want %q", cfg.App.Key, abs)
	}
}
