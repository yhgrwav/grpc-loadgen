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
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

var ErrReadConfig = errors.New("cannot read config")

// LoadFile reads the config file, applies defaults and validates the result.
func LoadFile(path string) (*MasterConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", ErrReadConfig, path, err)
	}

	return Parse(raw)
}

// Parse reads the config from raw YAML, applies defaults and validates the result.
func Parse(raw []byte) (*MasterConfig, error) {
	if err := checkNumbers(raw); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReadConfig, err)
	}

	var cfg MasterConfig
	if err := yaml.UnmarshalWithOptions(raw, &cfg, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReadConfig, err)
	}

	if err := cfg.resolve(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (m *MasterConfig) resolve() error {
	m.App.ResolveTLS()
	for i := range m.Load.Calls {
		m.Load.Calls[i].ResolveTimeout()
	}

	address, err := m.App.Target.CreateConnectionString()
	if err != nil {
		return err
	}
	m.App.Address = address

	return nil
}

func (m *MasterConfig) Validate() error {
	var errs []error

	if m.Name != nil && strings.TrimSpace(*m.Name) == "" {
		errs = append(errs, ErrEmptyName)
	}
	if err := m.Load.Validate(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
