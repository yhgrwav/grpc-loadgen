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

package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// metadataKey is what HTTP/2 carries as a header name once lowercased.
var metadataKey = regexp.MustCompile(`^[0-9a-z_.-]+$`)

// envRef is the only form read from the environment: a lone $ stays as written.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// resolveMetadata lowercases keys and fills ${NAME}. Errors name the key and
// the variable, never the value: it may be a secret.
func (a *App) resolveMetadata() error {
	if len(a.RawMetadata) == 0 {
		return nil
	}

	out := make(map[string]string, len(a.RawMetadata))

	var errs []error

	for raw, value := range a.RawMetadata {
		key := strings.ToLower(raw)

		switch {
		case !metadataKey.MatchString(key):
			errs = append(errs, fmt.Errorf("%w: key %q: only letters, digits, '-', '_' and '.'", ErrInvalidMetadata, raw))
			continue
		case strings.HasPrefix(key, "grpc-"):
			errs = append(errs, fmt.Errorf("%w: key %q: grpc- is reserved by gRPC", ErrInvalidMetadata, raw))
			continue
		}

		if _, dup := out[key]; dup {
			errs = append(errs, fmt.Errorf("%w: key %q twice: keys are case-insensitive", ErrInvalidMetadata, key))
			continue
		}

		var missing []string

		out[key] = envRef.ReplaceAllStringFunc(value, func(ref string) string {
			name := envRef.FindStringSubmatch(ref)[1]

			v, ok := os.LookupEnv(name)
			if !ok {
				missing = append(missing, name)
			}

			return v
		})

		for _, name := range missing {
			errs = append(errs, fmt.Errorf("%w: key %q: environment variable %s is not set", ErrInvalidMetadata, key, name))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	a.Metadata = out

	return nil
}

var sizeUnits = map[string]int64{
	"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30,
}

var sizePattern = regexp.MustCompile(`^(\d+)\s*([A-Za-z]+)$`)

// resolveMaxResponseSize reads a size such as 16MiB. A bare number is refused:
// 16 would be read as bytes by one person and megabytes by another.
func (a *App) resolveMaxResponseSize() error {
	if a.RawMaxResponseSize == nil {
		return nil
	}

	m := sizePattern.FindStringSubmatch(strings.TrimSpace(*a.RawMaxResponseSize))
	if m == nil {
		return fmt.Errorf("%w: %q", ErrInvalidMaxResponseSize, *a.RawMaxResponseSize)
	}

	unit, ok := sizeUnits[strings.ToLower(m[2])]
	n, err := strconv.ParseInt(m[1], 10, 64)

	// grpc-go takes the limit as an int and frames a message with a 32-bit
	// length; a limit it cannot hold is refused rather than wrapped.
	if !ok || err != nil || n <= 0 || n > math.MaxInt32/unit {
		return fmt.Errorf("%w: %q", ErrInvalidMaxResponseSize, *a.RawMaxResponseSize)
	}

	a.MaxResponseBytes = int(n * unit)

	return nil
}

func (a *App) validateTLSFiles() error {
	if !a.UseTLS && (a.CA != "" || a.Cert != "" || a.Key != "") {
		return ErrTLSFilesWithoutTLS
	}
	if (a.Cert == "") != (a.Key == "") {
		return ErrCertWithoutKey
	}

	return nil
}

// relativeTo makes the certificate paths relative to dir.
func (a *App) relativeTo(dir string) {
	for _, p := range []*string{&a.CA, &a.Cert, &a.Key} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(dir, *p)
		}
	}
}
