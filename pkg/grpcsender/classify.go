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
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// sizeLimit is how grpc-go words a message over a size limit, on either side
// (v1.84.0): "grpc: received message larger than max (N vs. M)". Pinned by a
// test, so an upgrade that rewords it goes red instead of silently turning
// these calls back into overload. A target not on grpc-go words it otherwise,
// and its refusal then reads as overload.
const sizeLimit = "larger than max"

// replyRefusals are how our grpc-go words a reply it received and would not
// accept for its encoding (v1.84.0, rpc_util.go and stream.go). Only our
// client writes them, so they are ours whatever came over the wire: the
// target's trailer may well have said OK.
var replyRefusals = []string{
	"grpc: failed to decompress",
	"grpc: Decompressor is not installed",
	"grpc: no decompressor available",
	"which is not allowed by AcceptCompressors",
	"message after decompression larger than max",
}

// refusedReply reports whether the client itself refused a reply that had
// arrived. answered says a status came over the wire: a size refusal with a
// status from the target is the target refusing our request instead.
func refusedReply(err error, answered bool) bool {
	msg := status.Convert(err).Message()
	for _, r := range replyRefusals {
		if strings.Contains(msg, r) {
			return true
		}
	}

	return !answered && status.Code(err) == codes.ResourceExhausted && strings.Contains(msg, sizeLimit)
}

// categorize maps a finished call onto the engine's categories. answered says
// whether a status came back over the wire: the same code means different
// things depending on who produced it. wentOut says whether the last attempt's
// request was written to the connection.
func categorize(err error, answered, wentOut bool) engine.Category {
	if err == nil {
		return engine.CategorySuccess
	}

	code := status.Code(err)

	// Checked before answered: a trailer saying OK can come with a code our
	// client set on the reply it refused.
	if refusedReply(err, answered) {
		return engine.CategoryBadResponse
	}

	// A request that does not fit will not fit at any rate: the request's
	// fault, not the load's. Only a target on grpc-go is recognised.
	if answered && code == codes.ResourceExhausted && strings.Contains(status.Convert(err).Message(), sizeLimit) {
		return engine.CategoryClientFault
	}

	if !answered {
		// No status came back. A deadline is still a bound on the latency. A
		// request that went out reached the other end, which reset the stream or
		// dropped the connection: it may have been processed. One that did not
		// go out reached no one: no connection, or the client's own stack
		// refused to send it.
		switch {
		case code == codes.DeadlineExceeded:
			return engine.CategoryTimeout
		case wentOut:
			return engine.CategoryCutOff
		case code == codes.Unavailable:
			return engine.CategoryUnreachable
		default:
			return engine.CategoryClientError
		}
	}

	switch code {
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied,
		codes.Unauthenticated, codes.FailedPrecondition, codes.OutOfRange, codes.Unimplemented:
		return engine.CategoryClientFault
	case codes.ResourceExhausted, codes.Unavailable:
		// UNAVAILABLE is also a proxy with no live upstream: the category says
		// what the status says, not what the target is.
		return engine.CategoryOverload
	case codes.DeadlineExceeded:
		return engine.CategoryTimeout
	default:
		// Internal, Unknown, DataLoss, a server that cancelled on its own side,
		// and Aborted: a conflict between concurrent changes, which load makes
		// more frequent but which is not a lack of capacity.
		return engine.CategoryServerFault
	}
}
