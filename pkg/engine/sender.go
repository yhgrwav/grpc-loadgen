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

package engine

import (
	"context"
	"time"
)

type Category int

const (
	// CategoryUnknown is the zero value: a sender that forgot to set Category
	// gets counted as a failure instead of silently passing as a success.
	CategoryUnknown Category = iota
	CategorySuccess
	// CategoryClientFault means the request will fail the same way again; it
	// usually points at the run's config, not at the target.
	CategoryClientFault
	CategoryServerFault
	CategoryTimeout
	// CategoryOverload means the target rejected the call for lack of
	// capacity, not because of anything wrong with the request itself.
	CategoryOverload
	// CategoryUnreachable means the call never reached the target and no reply
	// was coming: the connection was refused, dropped, or never established.
	// It carries no latency to speak of — a refused connection comes back in
	// microseconds — so it is counted apart from the distribution rather than
	// recorded as a fast response.
	CategoryUnreachable
)

func (c Category) String() string {
	switch c {
	case CategorySuccess:
		return "success"
	case CategoryClientFault:
		return "client fault"
	case CategoryServerFault:
		return "server fault"
	case CategoryTimeout:
		return "timeout"
	case CategoryOverload:
		return "overload"
	case CategoryUnreachable:
		return "unreachable"
	default:
		return "unknown"
	}
}

type Outcome struct {
	// SentAt is when the request actually went out on the wire, after the
	// sender was granted transport quota. Zero if the sender does not track
	// this; the worker pool then falls back to the moment Send was called.
	SentAt   time.Time
	DoneAt   time.Time
	Category Category
	// Err is the error as reported by the transport, including any text from
	// the target. Nil for CategorySuccess, non-nil otherwise. pkg/engine
	// does not inspect or print it; that is left to the caller.
	Err error
	// Response is the raw, undecoded response body, filled in only when
	// Request.KeepResponse is set.
	Response []byte
	// Code is the transport's own name for the outcome, carried through so the
	// report can print the fact next to the category. Category is a judgement
	// and sometimes a guess — gRPC's UNAVAILABLE covers overload, a failed
	// dependency and a rolling deploy alike — while this is what actually came
	// back. A string rather than a transport type: the engine stays independent
	// of the protocol.
	Code string
}

// Sender delivers one call to the target and reports what happened to it.
//
// A returned error means the attempt was never measured: the sender itself
// is unusable and the run cannot continue. Outcome.Category != CategorySuccess
// with a nil error means the call happened and failed; that is data about the
// target, and the run goes on.
//
// ctx being canceled is not a sender failure: it is the engine stopping the
// run. If cancellation is why Send returns an error, that error must wrap
// ctx.Err() (via %w), so callers can tell it apart with errors.Is.
//
// Implementations must be safe for concurrent use, and must apply
// req.Deadline as given rather than recompute it from the current time.
type Sender interface {
	Send(ctx context.Context, req Request) (Outcome, error)
}
