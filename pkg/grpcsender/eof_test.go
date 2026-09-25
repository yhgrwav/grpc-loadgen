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

package grpcsender

import (
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// grpc-go v1.84.0 can return a bare io.EOF from Invoke after a transparent
// retry whose answer beat the client's write of the body (1 in 3000 on
// go1.27.1). Until it gets an outcome of its own, it is pinned as what it is
// now: code Unknown set by the client, and — the request went out, no status
// was read — cut off.
func TestCategorize_BareEOFIsAnUnknownCodeTheClientSet(t *testing.T) {
	if got := status.Code(io.EOF); got != codes.Unknown {
		t.Fatalf("status.Code(io.EOF) = %v, want Unknown", got)
	}
	if got := categorize(io.EOF, false, true); got != engine.CategoryCutOff {
		t.Errorf("category %v, want cut off", got)
	}
}
