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

// sizeLimit is the part of grpc-go's message that both size errors carry
// (v1.84.0): "grpc: received message larger than max (N vs. M)". Pinned by a
// test, so an upgrade that rewords it goes red instead of silently turning
// these calls back into overload.
const sizeLimit = "larger than max"

// categorize maps a finished call onto the engine's categories. answered says
// whether the target sent a status of its own: the same code means different
// things depending on who produced it.
func categorize(err error, answered, wentOut bool) engine.Category {
	_ = wentOut

	if err == nil {
		return engine.CategorySuccess
	}

	code := status.Code(err)

	// A message over a size limit, whoever holds it: the client cutting a
	// reply off before the trailer, or the target refusing a request too big.
	// Both carry RESOURCE_EXHAUSTED, the code a target out of capacity also
	// uses, and only the text tells them apart. A request that does not fit
	// will not fit at any rate, so it is the request's fault, not the load's.
	if code == codes.ResourceExhausted && strings.Contains(status.Convert(err).Message(), sizeLimit) {
		return engine.CategoryClientFault
	}

	if !answered {
		// Nothing came back from the target. A deadline is still a bound on the
		// latency; anything else means the call never reached anyone.
		if code == codes.DeadlineExceeded {
			return engine.CategoryTimeout
		}

		return engine.CategoryUnreachable
	}

	switch code {
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied,
		codes.Unauthenticated, codes.FailedPrecondition, codes.OutOfRange, codes.Unimplemented:
		return engine.CategoryClientFault
	case codes.ResourceExhausted, codes.Unavailable, codes.Aborted:
		// Aborted is a concurrency conflict, which is what load produces; blaming
		// the caller for it would turn a load signal into a config error.
		return engine.CategoryOverload
	case codes.DeadlineExceeded:
		return engine.CategoryTimeout
	default:
		// Internal, Unknown, DataLoss, and a server that cancelled on its own side.
		return engine.CategoryServerFault
	}
}
