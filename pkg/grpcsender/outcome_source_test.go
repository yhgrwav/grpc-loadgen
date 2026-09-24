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
	"fmt"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// A call that went out and got no status back reached the other end: it was
// cut off, not unreachable. "Unreachable" says the target never saw the call;
// for a write that it may have processed, that is the wrong thing to say.
//
// Ground: contract — engine.CategoryCutOff is public.
func TestSend_WentOutNoStatusIsCutOffNotUnreachable(t *testing.T) {
	cases := []struct {
		name string
		send func(t *testing.T) engine.Outcome
		code codes.Code
		want engine.Category
	}{
		{"stream reset with INTERNAL_ERROR", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(fr *http2.Framer, id uint32, _ int) { _ = fr.WriteRSTStream(id, http2.ErrCodeInternal) }), 0)
		}, codes.Internal, engine.CategoryCutOff},
		// The other end took the request and dropped the connection.
		{"connection dropped after the request went out", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(*http2.Framer, uint32, int) {}, true), time.Second)
		}, codes.Unavailable, engine.CategoryCutOff},
		// The connection was gone before anything was written: nothing reached anyone.
		{"nothing answered", func(t *testing.T) engine.Outcome {
			return sendWithin(t, vanishedTarget(t), 0)
		}, codes.Unavailable, engine.CategoryUnreachable},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := c.send(t)
			if out.Code != c.code.String() {
				t.Fatalf("code = %q, want %q: the case no longer pins its code", out.Code, c.code)
			}
			if out.Category != c.want {
				t.Errorf("category = %v, want %v (sent at %v)", out.Category, c.want, out.SentAt)
			}
		})
	}
}

// The outcome and the code's source never contradict each other, for every
// code and every way a call can end. Unreachable and cut off mean no status came
// back, so their code is never the target's; overload and server fault are
// statuses that came back, so theirs always is. A later change to categorize
// that breaks this would put "unreachable" next to "sent by the target" again.
func TestCategorize_OutcomeAgreesWithTheCodesSource(t *testing.T) {
	sizeCut := status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5 vs. 4)")

	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		for _, err := range []error{status.Error(code, "x"), sizeCut} {
			if err == sizeCut && code != codes.ResourceExhausted {
				continue
			}
			for _, answered := range []bool{true, false} {
				for _, wentOut := range []bool{true, false} {
					name := fmt.Sprintf("%v/answered=%v/wentOut=%v/size=%v", code, answered, wentOut, err == sizeCut)
					got := categorize(err, answered, wentOut)

					switch got {
					case engine.CategoryUnreachable, engine.CategoryCutOff:
						if answered {
							t.Errorf("%s: %v, but a status came back", name, got)
						}
					case engine.CategoryOverload, engine.CategoryServerFault:
						if !answered {
							t.Errorf("%s: %v, but no status came back", name, got)
						}
					}
					if got == engine.CategoryUnreachable && wentOut {
						t.Errorf("%s: unreachable, but the request went out", name)
					}
					if !answered && wentOut && code != codes.DeadlineExceeded && err != sizeCut && got != engine.CategoryCutOff {
						t.Errorf("%s: %v, want cut off: went out, no status came back", name, got)
					}
				}
			}
		}
	}
}
