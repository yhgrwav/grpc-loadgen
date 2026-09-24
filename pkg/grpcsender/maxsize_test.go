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

	"google.golang.org/grpc"

	"github.com/yhgrwav/leettest/pkg/engine"
)

const mib = 1 << 20

// replying answers any method with size bytes, whatever it was sent.
func replying(size int) grpc.ServerOption {
	return grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		var in []byte
		if err := stream.RecvMsg(&in); err != nil {
			return err
		}

		out := make([]byte, size)

		return stream.SendMsg(&out)
	})
}

func sendTo(t *testing.T, replySize, limit int) engine.Outcome {
	t.Helper()

	opts := listen(t, &seeingTarget{}, grpc.ForceServerCodec(rawCodec{}), replying(replySize))
	opts.MaxResponseBytes = limit
	sender := connected(t, opts)

	req := request(time.Now())
	req.Method = "/leettest.test.Big/Get"

	out, err := sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	return out
}

// Ground: signal grpc-go v1.84.0 — without the option the limit is grpc-go's default of 4 MiB,
// which it documents only in a comment; a change there changes what a run can receive.
func TestMaxResponse_WithoutTheOptionAReplyOverFourMiBIsARequestError(t *testing.T) {
	if out := sendTo(t, 4*mib+1, 0); out.Category != engine.CategoryClientFault {
		t.Errorf("category %v, want a request error (%v)", out.Category, out.Err)
	}
	if out := sendTo(t, 4*mib-64, 0); out.Category != engine.CategorySuccess {
		t.Errorf("a reply under 4 MiB: category %v (%v)", out.Category, out.Err)
	}
}

// Ground: contract — Options.MaxResponseBytes is public: a raised limit takes the reply.
func TestMaxResponse_ARaisedLimitTakesALargerReply(t *testing.T) {
	if out := sendTo(t, 5*mib, 8*mib); out.Category != engine.CategorySuccess {
		t.Errorf("category %v, want success under an 8 MiB limit (%v)", out.Category, out.Err)
	}
}

// Ground: contract — the raised limit is still a limit.
func TestMaxResponse_AReplyOverTheRaisedLimitIsARequestError(t *testing.T) {
	if out := sendTo(t, 8*mib+1, 8*mib); out.Category != engine.CategoryClientFault {
		t.Errorf("category %v, want a request error (%v)", out.Category, out.Err)
	}
}
