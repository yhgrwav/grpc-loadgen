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
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// The grid is part of the output contract: moving a code to another category
// changes the numbers under the same names. Keep it in step with the table in
// the README.
func TestCategorize_EveryCodeFromEitherSide(t *testing.T) {
	type want struct{ fromTarget, clientSent, clientUnsent engine.Category }

	var (
		failure      = want{engine.CategoryServerFault, engine.CategoryCutOff, engine.CategoryClientError}
		overload     = want{engine.CategoryOverload, engine.CategoryCutOff, engine.CategoryClientError}
		requestError = want{engine.CategoryClientFault, engine.CategoryCutOff, engine.CategoryClientError}
	)

	grid := map[codes.Code]want{
		codes.Canceled:           failure,
		codes.Unknown:            failure,
		codes.Internal:           failure,
		codes.DataLoss:           failure,
		codes.Aborted:            failure, // a conflict between concurrent changes, not capacity
		codes.ResourceExhausted:  overload,
		codes.Unavailable:        {engine.CategoryOverload, engine.CategoryCutOff, engine.CategoryUnreachable},
		codes.InvalidArgument:    requestError,
		codes.NotFound:           requestError,
		codes.AlreadyExists:      requestError,
		codes.PermissionDenied:   requestError,
		codes.Unauthenticated:    requestError,
		codes.FailedPrecondition: requestError,
		codes.OutOfRange:         requestError,
		codes.Unimplemented:      requestError,
		codes.DeadlineExceeded:   {engine.CategoryTimeout, engine.CategoryTimeout, engine.CategoryTimeout},
	}
	if len(grid) != 16 {
		t.Fatalf("grid has %d codes, want every code but OK", len(grid))
	}

	for code, w := range grid {
		err := status.Error(code, "x")
		for _, c := range []struct {
			name              string
			answered, wentOut bool
			want              engine.Category
		}{
			{"from the target", true, true, w.fromTarget},
			{"set by the client after sending", false, true, w.clientSent},
			{"set by the client before sending", false, false, w.clientUnsent},
		} {
			if got := categorize(err, c.answered, c.wentOut); got != c.want {
				t.Errorf("%s %s: category %v, want %v", code, c.name, got, c.want)
			}
		}
	}

	if got := categorize(nil, true, true); got != engine.CategorySuccess {
		t.Errorf("OK: category %v, want success", got)
	}
}

// The property of #85 over the whole grid: a category that says the target
// or a proxy answered needs a code that came over the wire, and one that says
// the client ended the call needs a code the client set.
func TestCategorize_TheCategoryNeverContradictsTheCodesSource(t *testing.T) {
	fromWire := map[engine.Category]bool{
		engine.CategoryClientFault: true, engine.CategoryOverload: true, engine.CategoryServerFault: true,
	}
	fromClient := map[engine.Category]bool{
		engine.CategoryCutOff: true, engine.CategoryUnreachable: true,
		engine.CategoryClientError: true, engine.CategoryBadResponse: true,
	}

	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		for _, answered := range []bool{true, false} {
			for _, wentOut := range []bool{true, false} {
				if answered && !wentOut {
					continue
				}
				got := categorize(status.Error(code, "x"), answered, wentOut)
				if fromWire[got] && !answered {
					t.Errorf("%s set by the client: category %v says the other end answered", code, got)
				}
				if fromClient[got] && answered {
					t.Errorf("%s from the wire: category %v says the client ended the call", code, got)
				}
			}
		}
	}
}

// Size limits and undecodable bodies are told apart only by grpc-go's own
// text. From our client that text is known; from a target not on grpc-go it
// is not, and the call lands on overload — the wrong side, pinned here and
// said in the README.
func TestCategorize_MessagesThatDidNotFit(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		answered bool
		want     engine.Category
	}{
		{"target refused a request over its limit (grpc-go text)", status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5 vs. 4)"), true, engine.CategoryClientFault},
		{"target refused a request over its limit (other text)", status.Error(codes.ResourceExhausted, "request entity too large"), true, engine.CategoryOverload},
		{"a reply over our max_response_size", status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5 vs. 4)"), false, engine.CategoryBadResponse},
		{"a reply we could not decompress", status.Error(codes.Internal, "grpc: failed to decompress the received message: gzip: invalid header"), false, engine.CategoryBadResponse},
		{"a reply compressed with an encoding we lack", status.Error(codes.Internal, "grpc: Decompressor is not installed for grpc-encoding \"br\""), false, engine.CategoryBadResponse},
		{"a stream reset after the reply's headers", status.Error(codes.Internal, "stream terminated by RST_STREAM with error code: INTERNAL_ERROR"), false, engine.CategoryCutOff},
	} {
		if got := categorize(c.err, c.answered, true); got != c.want {
			t.Errorf("%s: category %v, want %v", c.name, got, c.want)
		}
	}
}
