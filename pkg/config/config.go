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
	"net"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidIP       = errors.New("got empty IP")
	ErrInvalidPort     = errors.New("got invalid port")
	ErrNoCalls         = errors.New("no calls configured")
	ErrInvalidMethod   = errors.New("method must look like package.Service/Method")
	ErrInvalidRPS      = errors.New("rps must be positive")
	ErrInvalidDuration = errors.New("duration must be positive")
	ErrInvalidWarmup   = errors.New("warmup must not be negative")
	ErrEmptyName       = errors.New("name must not be empty: leave it out to use the service or file name")
	ErrInvalidTimeout  = errors.New("timeout must be positive: without one, requests to a hung target pile up until the in-flight cap ends the run")
)

type MasterConfig struct {
	// Name is what the header calls the run; nil leaves it to the service or file.
	Name *string `yaml:"name"`
	App  App     `yaml:"app"`
	Load Load    `yaml:"load"`
}

type App struct {
	Target  ConnectionStringTarget `yaml:"target"`
	Address ConnectionString       `yaml:"-"`
	TLS     *bool                  `yaml:"tls"`
	UseTLS  bool                   `yaml:"-"`
}

type ConnectionStringTarget struct {
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}

type ConnectionString string

type Load struct {
	Warmup time.Duration `yaml:"warmup"`
	Calls  []Call        `yaml:"calls"`
}

type Call struct {
	Method   string        `yaml:"method"`
	RPS      int           `yaml:"rps"`
	Duration time.Duration `yaml:"duration"`
	// Data is the request body as written, built into the method's message
	// before the run; nil sends an empty message.
	Data any `yaml:"data"`
	// RawTimeout is the field as written; nil means it was left out.
	RawTimeout *time.Duration `yaml:"timeout"`
	Timeout    time.Duration  `yaml:"-"`
}

// DefaultTimeout applies to a call that sets none. It is short on purpose: in an
// open model a hung target holds rps × timeout requests in flight, and 2s keeps
// 2500 RPS under the default cap of 5000.
const DefaultTimeout = 2 * time.Second

// ResolveTimeout fills Timeout from the field or the default.
func (c *Call) ResolveTimeout() {
	if c.RawTimeout == nil {
		c.Timeout = DefaultTimeout

		return
	}
	c.Timeout = *c.RawTimeout
}

func (c ConnectionStringTarget) CreateConnectionString() (ConnectionString, error) {
	if c.IP == "" {
		return "", ErrInvalidIP
	}
	if c.Port < 1 || c.Port > 65535 {
		return "", fmt.Errorf("%w: %d", ErrInvalidPort, c.Port)
	}
	return ConnectionString(net.JoinHostPort(c.IP, strconv.Itoa(c.Port))), nil
}

func (a *App) ResolveTLS() {
	if a.TLS == nil {
		a.UseTLS = true
		return
	}
	a.UseTLS = *a.TLS
}

func (l Load) Validate() error {
	var errs []error

	if l.Warmup < 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidWarmup, l.Warmup))
	}
	if len(l.Calls) == 0 {
		errs = append(errs, ErrNoCalls)
	}
	for i, call := range l.Calls {
		if err := call.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("call %d: %w", i, err))
		}
	}

	return errors.Join(errs...)
}

func (c Call) Validate() error {
	var errs []error

	if !isMethodName(c.Method) {
		errs = append(errs, fmt.Errorf("%w: %q", ErrInvalidMethod, c.Method))
	}
	if c.RPS < 1 {
		errs = append(errs, fmt.Errorf("%w: %d", ErrInvalidRPS, c.RPS))
	}
	if c.Duration <= 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidDuration, c.Duration))
	}
	if c.RawTimeout != nil && *c.RawTimeout <= 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidTimeout, *c.RawTimeout))
	}

	return errors.Join(errs...)
}

func isMethodName(method string) bool {
	service, name, found := strings.Cut(method, "/")

	return found &&
		name != "" &&
		strings.Contains(service, ".") &&
		!strings.HasPrefix(service, ".") &&
		!strings.HasSuffix(service, ".")
}
