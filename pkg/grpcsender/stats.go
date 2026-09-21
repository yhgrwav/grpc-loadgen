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
	"time"

	"google.golang.org/grpc/stats"
)

type callKey struct{}

// shared is a deliberate bug: one record for every call.
var shared callStats

// callStats is what one call records about itself while the transport works.
// Send puts a fresh one in the context; the handler fills it in. Each call has
// its own, so nothing is shared between goroutines.
type callStats struct {
	sentAt time.Time
	doneAt time.Time
	// answered is true once the target's trailer arrives. It is the only way to
	// tell a served status from a call that never reached anyone: gRPC reports
	// both as UNAVAILABLE, and a refused connection carries no latency worth
	// recording.
	answered bool
}

// handler collects per-call timings. One instance serves the whole connection;
// the state lives in the context.
type handler struct{}

func (handler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

func (handler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }

func (handler) HandleConn(context.Context, stats.ConnStats) {}

func (handler) HandleRPC(ctx context.Context, rpc stats.RPCStats) {
	call, ok := ctx.Value(callKey{}).(*callStats)
	if !ok {
		return
	}

	switch v := rpc.(type) {
	case *stats.OutPayload:
		// SentTime is when the request went out on the wire, after the transport
		// granted stream quota. Taking it here rather than with time.Now() in the
		// worker keeps the Go scheduler's delay out of the measurement.
		call.sentAt = v.SentTime
	case *stats.InTrailer:
		call.answered = true
	case *stats.End:
		call.doneAt = v.EndTime
	}
}
