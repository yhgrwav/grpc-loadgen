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
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/pkg/config"
)

// A refused method check is decided by its code. Unauthenticated with
// metadata set: the credentials sent were refused, and a run would only show
// a config mistake as the target's result — stop. PermissionDenied: the
// credentials were accepted but may not reach reflection while still serving
// calls — the run goes on, as decided in #62. Unauthenticated without
// metadata: the run goes on too, and the note says what is missing. The
// target's message is never repeated: it may echo what was sent.
func TestAttachData_ARefusedCheckIsDecidedByItsCode(t *testing.T) {
	cases := []struct {
		name     string
		code     codes.Code
		metadata map[string]string
		stops    bool
		note     string
	}{
		{"unauthenticated with metadata", codes.Unauthenticated, map[string]string{"authorization": "x"}, true, ""},
		{"permission denied with metadata", codes.PermissionDenied, map[string]string{"authorization": "x"}, false, "PermissionDenied"},
		{"permission denied without metadata", codes.PermissionDenied, nil, false, "PermissionDenied"},
		{"unauthenticated without metadata", codes.Unauthenticated, nil, false, "target requires credentials; app.metadata is not set"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, calls := loadOf(config.Call{Method: "wallet.v1.Wallet/One"})
			cfg.App.Metadata = c.metadata
			resolver := &fakeResolver{err: status.Error(c.code, "echo s3cret-token")}

			unchecked, err := AttachData(t.Context(), resolver, cfg, calls)

			if got := errors.Is(err, ErrCredentialsRejected); got != c.stops {
				t.Fatalf("stopped = %v (err %v), want %v", got, err, c.stops)
			}
			if c.stops {
				if strings.Contains(err.Error(), "s3cret") {
					t.Errorf("the error repeats the target's message: %v", err)
				}
				if len(unchecked) != 0 {
					t.Errorf("unchecked %v alongside a stop", unchecked)
				}

				return
			}

			if err != nil || len(unchecked) != 1 {
				t.Fatalf("err %v, unchecked %v; want the run to go on with the method unchecked", err, unchecked)
			}

			var out strings.Builder
			PrintReport(&out, "localhost:50051", RunReport{Unchecked: unchecked})
			if !strings.Contains(out.String(), c.note) {
				t.Errorf("the note does not say %q:\n%s", c.note, out.String())
			}
		})
	}
}
