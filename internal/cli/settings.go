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

package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

const settingsDir = "grpc-loadgen"

// Settings holds what the tool remembers between runs.
type Settings struct {
	Lang    string `yaml:"lang"`
	Mode    string `yaml:"mode"`
	Palette string `yaml:"palette"`

	path string
}

// LoadSettings reads the stored settings, returning defaults when there are none.
func LoadSettings() (*Settings, error) {
	path, err := settingsPath()
	if err != nil {
		return nil, err
	}

	settings := &Settings{path: path}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(raw, settings); err != nil {
		return nil, err
	}

	settings.path = path

	return settings, nil
}

// Configured reports whether the tool has already been set up.
func (s *Settings) Configured() bool {
	return s.Lang != ""
}

// Save writes the settings back to disk.
func (s *Settings) Save() error {
	if s.path == "" {
		path, err := settingsPath()
		if err != nil {
			return err
		}
		s.path = path
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	raw, err := yaml.Marshal(s)
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, raw, 0o600)
}

// Path returns the file the settings live in.
func (s *Settings) Path() string {
	return s.path
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, settingsDir, "settings.yaml"), nil
}
