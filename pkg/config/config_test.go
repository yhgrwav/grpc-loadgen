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
	"testing"
)

func TestCreateConnectionString(t *testing.T) {
	tests := []struct {
		name    string
		target  ConnectionStringTarget
		want    ConnectionString
		wantErr error
	}{
		{
			name:   "host and port",
			target: ConnectionStringTarget{IP: "localhost", Port: 50051},
			want:   "localhost:50051",
		},
		{
			name:   "lowest valid port",
			target: ConnectionStringTarget{IP: "127.0.0.1", Port: 1},
			want:   "127.0.0.1:1",
		},
		{
			name:   "highest valid port",
			target: ConnectionStringTarget{IP: "127.0.0.1", Port: 65535},
			want:   "127.0.0.1:65535",
		},
		{
			name:    "empty ip",
			target:  ConnectionStringTarget{Port: 50051},
			wantErr: ErrInvalidIP,
		},
		{
			name:    "zero port",
			target:  ConnectionStringTarget{IP: "localhost"},
			wantErr: ErrInvalidPort,
		},
		{
			name:    "negative port",
			target:  ConnectionStringTarget{IP: "localhost", Port: -1},
			wantErr: ErrInvalidPort,
		},
		{
			name:    "port above range",
			target:  ConnectionStringTarget{IP: "localhost", Port: 65536},
			wantErr: ErrInvalidPort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.target.CreateConnectionString()

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTLS(t *testing.T) {
	enabled, disabled := true, false

	tests := []struct {
		name string
		tls  *bool
		want bool
	}{
		{name: "unset defaults to TLS", tls: nil, want: true},
		{name: "explicitly enabled", tls: &enabled, want: true},
		{name: "explicitly disabled", tls: &disabled, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := App{TLS: tt.tls}

			app.ResolveTLS()

			if app.UseTLS != tt.want {
				t.Errorf("UseTLS = %v, want %v", app.UseTLS, tt.want)
			}
		})
	}
}
