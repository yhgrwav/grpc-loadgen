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
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/stats"
)

type callKey struct{}

// callTimes is what one call recorded about itself, as it stood when it was
// read.
type callTimes struct {
	// begunAt is when grpc-go began the call; pickedAt, when it got a
	// connection after waiting for one, zero if it did not wait.
	begunAt  time.Time
	pickedAt time.Time
	// streamFull says every stream the target allows was open at some moment
	// between the start of the wait for one and the headers going out.
	streamFull bool
	// headerAt is when the stream's headers went out. grpc-go emits OutHeader
	// inside NewStream after stream quota is granted, so a call without it
	// never got a stream.
	headerAt time.Time
	sentAt   time.Time
	doneAt   time.Time
	// answered is true once the target's trailer arrives. It is the only way to
	// tell a served status from a call that never reached anyone: gRPC reports
	// both as UNAVAILABLE, and a refused connection carries no latency worth
	// recording.
	answered bool
}

// callStats is where one call's timings are collected while the transport
// works. Send puts a fresh one in the context; the handler fills it in.
//
// Each call has its own, but not one goroutine: grpc-go reports headers and
// trailers from the transport's reader, and a call cut off by its deadline
// returns from Invoke while the target's status is still on its way. The
// reader then writes into the same struct Send is reading, so both go through
// the lock — a torn time.Time would put a moment in the report that never
// happened.
type callStats struct {
	mu    sync.Mutex
	times callTimes
}

// read copies the timings recorded so far. What arrives later is not part of
// the call: the caller had already given up on it.
func (c *callStats) read() callTimes {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.times
}

// handler collects per-call timings. One instance serves the whole connection;
// the state lives in the context.
type handler struct {
	// streams counts the streams open on the connection. Nil in tests that
	// feed events by hand.
	streams *streamGauge
}

func (handler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

func (handler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (handler) HandleConn(context.Context, stats.ConnStats) {}

func (h handler) HandleRPC(ctx context.Context, rpc stats.RPCStats) {
	call, ok := ctx.Value(callKey{}).(*callStats)
	if !ok {
		return
	}

	call.mu.Lock()
	defer call.mu.Unlock()

	switch v := rpc.(type) {
	case *stats.Begin:
		call.times.begunAt = v.BeginTime
	case *stats.DelayedPickComplete:
		// The wait for a stream starts once there is a connection.
		call.times.pickedAt = time.Now()
	case *stats.OutHeader:
		// OutHeader carries no time of its own; the call is synchronous at the
		// point the headers are handed to the transport.
		now := time.Now()
		call.times.headerAt = now
		if h.streams != nil {
			// Asked before this stream counts: whether the wait met a full
			// connection, not whether this call filled it.
			call.times.streamFull = h.streams.fullSince(call.times.waitFrom())
			h.streams.opened(now)
		}
	case *stats.OutPayload:
		// SentTime is when the request went out on the wire, after the transport
		// granted stream quota. Taking it here rather than with time.Now() in the
		// worker keeps the Go scheduler's delay out of the measurement.
		call.times.sentAt = v.SentTime
	case *stats.InTrailer:
		call.times.answered = true
	case *stats.End:
		if h.streams != nil && !call.times.headerAt.IsZero() {
			h.streams.closed(time.Now())
		}
		call.times.doneAt = v.EndTime
	}
}

// waitFrom is when the call began to wait for a stream: once it had a
// connection, at its pick if the pick waited, at its start otherwise.
func (t callTimes) waitFrom() time.Time {
	if t.pickedAt.After(t.begunAt) {
		return t.pickedAt
	}

	return t.begunAt
}

// streamWait is how long a call that got its headers out waited for a stream.
// Zero unless the connection was full at some moment of that wait: below the
// limit the quota was there, and whatever held the headers back was on our
// side — CI saw 1–3 calls of 600 held over 1ms at 300 rps with no limit.
func (t callTimes) streamWait() time.Duration {
	if t.headerAt.IsZero() || t.begunAt.IsZero() || !t.streamFull {
		return 0
	}

	return max(0, t.headerAt.Sub(t.waitFrom()))
}
