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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrNoSender            = errors.New("worker pool has no sender")
	ErrInvalidInFlightCap  = errors.New("in-flight cap must be positive")
	ErrInFlightCapExceeded = errors.New("in-flight cap exceeded")
)

type Sender interface {
	Send(ctx context.Context, req Request) error
}

type Result struct {
	Method      string
	ScheduledAt time.Time
	SentAt      time.Time
	DoneAt      time.Time
	Err         error
}

func (r Result) Latency() time.Duration {
	return r.DoneAt.Sub(r.ScheduledAt)
}

func (r Result) ServiceTime() time.Duration {
	return r.DoneAt.Sub(r.SentAt)
}

type WorkerPool struct {
	Sender      Sender
	MaxInFlight int

	inFlight atomic.Int64
}

func NewWorkerPool(sender Sender, maxInFlight int) *WorkerPool {
	return &WorkerPool{
		Sender:      sender,
		MaxInFlight: maxInFlight,
	}
}

func (p *WorkerPool) InFlight() int {
	return int(p.inFlight.Load())
}

func (p *WorkerPool) Run(ctx context.Context, in <-chan Request, out chan<- Result) error {
	if p.Sender == nil {
		return ErrNoSender
	}
	if p.MaxInFlight < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidInFlightCap, p.MaxInFlight)
	}

	sendCtx, abortSends := context.WithCancel(ctx)
	defer abortSends()

	inFlight := make(chan struct{}, p.MaxInFlight)

	var wg sync.WaitGroup

	finish := func(err error) error {
		if err != nil {
			abortSends()
		}
		wg.Wait()

		return err
	}

	for {
		var req Request

		select {
		case <-ctx.Done():
			return finish(ctx.Err())
		case r, ok := <-in:
			if !ok {
				return finish(nil)
			}
			req = r
		}

		select {
		case inFlight <- struct{}{}:
		case <-ctx.Done():
			return finish(ctx.Err())
		default:
			return finish(fmt.Errorf("%w: %d", ErrInFlightCapExceeded, p.MaxInFlight))
		}

		p.inFlight.Add(1)
		wg.Add(1)

		go func() {
			defer wg.Done()
			defer func() {
				p.inFlight.Add(-1)
				<-inFlight
			}()

			p.send(sendCtx, req, out)
		}()
	}
}

func (p *WorkerPool) send(ctx context.Context, req Request, out chan<- Result) {
	sentAt := time.Now()
	err := p.Sender.Send(ctx, req)

	result := Result{
		Method:      req.Method,
		ScheduledAt: req.ScheduledAt,
		SentAt:      sentAt,
		DoneAt:      time.Now(),
		Err:         err,
	}

	select {
	case out <- result:
	case <-ctx.Done():
	}
}
