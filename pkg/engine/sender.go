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
	// CategoryOverload means an error status came back for lack of capacity,
	// not because of anything wrong with the request itself. The status may
	// come from a proxy in front of the target: nginx answers 14 when it has no
	// live upstream.
	CategoryOverload
	// CategoryUnreachable means the call never reached the target and no reply
	// was coming: the connection was refused, dropped, or never established.
	// It carries no latency to speak of — a refused connection comes back in
	// microseconds — so it is counted apart from the distribution rather than
	// recorded as a fast response.
	CategoryUnreachable
	// CategoryAborted means the run was aborted while the call was in flight.
	// It is no fault of the target: the call is known only to have lasted at
	// least until the abort, and is recorded as censored at that moment.
	CategoryAborted
	// CategoryCutOff means the request went out and no status came back: the
	// other end, the target or a proxy, reset the stream or dropped the
	// connection. It may have been processed. There is no status to time, so
	// it carries no latency, and it does not count as an answer.
	CategoryCutOff
	// CategoryClientError means the client stack refused to send: a request it
	// could not encode, a codec or interceptor that failed. Nothing reached the
	// target, and the same call will fail again.
	CategoryClientError
	// CategoryBadResponse means a reply reached the client and the client did
	// not accept it: over its size limit, or a body it could not decompress.
	// The target answered, so the latency is real; the call is not a success.
	CategoryBadResponse
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
	case CategoryAborted:
		return "aborted"
	case CategoryCutOff:
		return "cut off"
	default:
		return "unknown"
	}
}

type Outcome struct {
	// SentAt is when the request actually went out on the wire, after the
	// sender was granted transport quota. Zero if the sender does not track
	// this; the worker pool then falls back to the moment Send was called.
	SentAt time.Time
	// NotSent marks a timeout whose request never went out. The engine tells
	// whose fault it was by who ate more of the budget: the generator's lag
	// or the wait on the connection. False when the sender does not track this.
	NotSent bool
	// NotSentOn is what an unsent call was waiting for when its deadline came,
	// if the generator's lag was not the larger part of it.
	NotSentOn Blocker
	// StreamWait is how long a call that went out waited for a free stream on
	// a ready connection: part of its latency the target never saw.
	StreamWait time.Duration
	// ConnWait is how long the call waited for a ready connection: name
	// resolution and picking a transport, summed over its attempts. It does
	// not overlap StreamWait. For a call that went out it is the whole wait;
	// for one that did not it is a lower bound — the attempt the deadline cut
	// short never reported its wait. The gap between one attempt failing and
	// the next beginning is in neither.
	//
	// Each attempt counts from its own start, except the first after a wait
	// for the resolver, which counts from the moment the call was handed to
	// the transport. That first stretch then also holds whatever the caller's
	// own interceptors spent: an overstatement. Without a resolver wait the
	// stretch before the attempt starts is the transport's own work — 0.5 to
	// 2.2 ms measured under -race on two CPUs — and is not counted.
	ConnWait time.Duration
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
	// CodeFromTarget is true when Code came back over the wire, from the target
	// or a proxy in front of it; false when the transport set it itself: nothing
	// answered, the stream was reset, our own deadline ran out. The last attempt
	// of a transparently retried call decides.
	CodeFromTarget bool
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

// Blocker is what a call that never went out was waiting for.
type Blocker int

const (
	// BlockedUnknown is the zero value: a sender that left NotSentOn unset.
	// It is a defect of the sender, so such a call is in NotSent but in none
	// of its causes, and their sum falls short of it.
	BlockedUnknown Blocker = iota
	// BlockedOnConnection is a connection the sender cannot prove was ready
	// the whole time: then the call is not blamed on streams.
	BlockedOnConnection
	// BlockedOnStream is a ready connection with every stream the target
	// allows already in use.
	BlockedOnStream
	// BlockedOnGenerator is a ready connection with streams to spare: what held
	// the call back was on the generator's side.
	BlockedOnGenerator
)

// Connections is what a sender knows about the connections it ran over.
type Connections struct {
	// Open is how many connections carried calls at once.
	Open int
	// Reconnects counts successful handshakes after the first.
	Reconnects int
	// LimitAnnounced says the target named a stream limit in the first
	// SETTINGS of the last handshake; without it the client has no limit of
	// its own.
	LimitAnnounced bool
	// FirstLimit and LastLimit are the limits announced at the first and the
	// last handshake; LimitChanges counts handshakes that announced a limit
	// different from the one before.
	FirstLimit   uint32
	LastLimit    uint32
	LimitChanges int
}

// ConnectionReporter is a Sender that can say what connections it used. The
// engine asks once, when the run is over; false means the sender saw nothing
// it can vouch for, and the report then says nothing about connections.
type ConnectionReporter interface {
	Connections() (Connections, bool)
}

// Name is the category's name in machine-readable output. The set is a
// contract: a rename breaks scripts that read it.
func (c Category) Name() string {
	return ""
}
