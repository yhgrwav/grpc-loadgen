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
)

var (
	ErrInvalidIP   = errors.New("got empty IP")
	ErrInvalidPort = errors.New("got invalid port")
)

type MasterConfig struct {
	App App `yaml:"app"`
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

func (c ConnectionStringTarget) CreateConnectionString() (ConnectionString, error) {
	if c.IP == "" {
		return "", ErrInvalidIP
	}
	if c.Port < 1 || c.Port > 65535 {
		return "", fmt.Errorf("%w: %d", ErrInvalidPort, c.Port)
	}
	return ConnectionString(fmt.Sprintf("%s:%d", c.IP, c.Port)), nil
}

func (a *App) ResolveTLS() {
	if a.TLS == nil {
		a.UseTLS = true
		return
	}
	a.UseTLS = *a.TLS
}
