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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

// categorize maps a finished call onto the engine's categories. answered says
// whether the target sent a status of its own: the same code means different
// things depending on who produced it.
func categorize(err error, answered bool) engine.Category {
	if err == nil {
		return engine.CategorySuccess
	}

	code := status.Code(err)

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
