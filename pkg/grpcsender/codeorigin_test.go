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
	"time"

	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// A code is the target's only when a status came over the wire. grpc-go makes up
// the same codes on the client: Unavailable when nothing answered, DeadlineExceeded
// at our own deadline. Printed together, a code made here reads as the target's.
//
// Ground: contract — engine.Outcome.CodeFromTarget is public.
func TestSend_TellsACodeTheTargetSentFromOneTheClientMade(t *testing.T) {
	late := request(time.Now())
	late.Deadline = time.Now().Add(50 * time.Millisecond)

	past := request(time.Now())
	past.Deadline = time.Now().Add(-time.Millisecond)

	served := func(c codes.Code) func(t *testing.T) engine.Outcome {
		return func(t *testing.T) engine.Outcome {
			out, err := dialTarget(t, &target{code: c}).Send(bounded(t), request(time.Now()))
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			return out
		}
	}

	cases := []struct {
		name       string
		send       func(t *testing.T) engine.Outcome
		code       codes.Code
		fromTarget bool
	}{
		{"target sent Unavailable", served(codes.Unavailable), codes.Unavailable, true},
		{"target sent DeadlineExceeded", served(codes.DeadlineExceeded), codes.DeadlineExceeded, true},
		{"nothing answered: client's Unavailable", func(t *testing.T) engine.Outcome {
			out, err := vanishedTarget(t).Send(bounded(t), request(time.Now()))
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			return out
		}, codes.Unavailable, false},
		{"our deadline ran out: client's DeadlineExceeded", func(t *testing.T) engine.Outcome {
			out, err := dialTarget(t, &target{delay: time.Second}).Send(bounded(t), late)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			return out
		}, codes.DeadlineExceeded, false},
		{"deadline already past, never sent", func(t *testing.T) engine.Outcome {
			out, err := dialTarget(t, &target{}).Send(bounded(t), past)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			return out
		}, codes.DeadlineExceeded, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := c.send(t)
			if out.Code != c.code.String() {
				t.Fatalf("code = %q, want %q: the case no longer pins its code", out.Code, c.code)
			}
			if out.CodeFromTarget != c.fromTarget {
				t.Errorf("CodeFromTarget = %v, want %v", out.CodeFromTarget, c.fromTarget)
			}
		})
	}
}
