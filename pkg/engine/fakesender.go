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
	"math/rand/v2"
	"time"
)

// FakeSender answers after a delay instead of calling a real service.
type FakeSender struct {
	Delay     time.Duration
	Jitter    time.Duration
	FailRatio float64
}

func (f FakeSender) Send(ctx context.Context, req Request) (Outcome, error) {
	sendCtx := ctx
	if !req.Deadline.IsZero() {
		var cancel context.CancelFunc
		sendCtx, cancel = context.WithDeadline(ctx, req.Deadline)
		defer cancel()
	}

	delay := f.Delay
	if f.Jitter > 0 {
		delay += time.Duration(rand.Int64N(int64(f.Jitter)))
	}

	sentAt := time.Now()

	select {
	case <-time.After(delay):
	case <-sendCtx.Done():
		if ctx.Err() != nil {
			return Outcome{}, ctx.Err()
		}

		return Outcome{Category: CategoryTimeout, Err: sendCtx.Err(), SentAt: sentAt, DoneAt: time.Now()}, nil
	}

	doneAt := time.Now()

	if f.FailRatio > 0 && rand.Float64() < f.FailRatio {
		return Outcome{Category: CategoryServerFault, Err: ErrFakeFailure, SentAt: sentAt, DoneAt: doneAt}, nil
	}

	return Outcome{Category: CategorySuccess, SentAt: sentAt, DoneAt: doneAt}, nil
}
