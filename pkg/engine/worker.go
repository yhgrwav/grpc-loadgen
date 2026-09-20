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

type Result struct {
	Method      string
	ScheduledAt time.Time
	BegunAt     time.Time
	Outcome
}

func (r Result) Latency() time.Duration {
	return r.DoneAt.Sub(r.ScheduledAt)
}

func (r Result) QueueTime() time.Duration {
	return r.BegunAt.Sub(r.ScheduledAt)
}

func (r Result) TransportWait() time.Duration {
	return r.SentAt.Sub(r.BegunAt)
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

func (p *WorkerPool) inFlightCount() int {
	return int(p.inFlight.Load())
}

func (p *WorkerPool) Run(ctx context.Context, in <-chan Request, out chan<- Result) error {
	if p.Sender == nil {
		return ErrNoSender
	}
	if p.MaxInFlight < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidInFlightCap, p.MaxInFlight)
	}

	r := newPoolRun(ctx, p.MaxInFlight)
	defer r.abort()

	return r.dispatch(p, in, out)
}

// poolRun tracks the state of a single Run call: the context sends are made
// under, the in-flight slots, and the first fatal error seen by any worker.
type poolRun struct {
	sendCtx context.Context
	abort   context.CancelFunc
	slots   chan struct{}

	wg sync.WaitGroup

	mu       sync.Mutex
	fatalErr error
}

func newPoolRun(ctx context.Context, maxInFlight int) *poolRun {
	sendCtx, abort := context.WithCancel(ctx)

	return &poolRun{
		sendCtx: sendCtx,
		abort:   abort,
		slots:   make(chan struct{}, maxInFlight),
	}
}

// fail records err as the run's outcome unless one was already recorded, and
// stops every in-flight and future send.
func (r *poolRun) fail(err error) {
	r.mu.Lock()
	if r.fatalErr == nil {
		r.fatalErr = err
	}
	r.mu.Unlock()

	r.abort()
}

func (r *poolRun) finish(err error) error {
	if err != nil {
		r.fail(err)
	}
	r.wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fatalErr != nil {
		return r.fatalErr
	}
	if err != nil {
		return err
	}

	// A send may have swallowed its own error as a side effect of this same
	// cancellation; make sure the cancellation still surfaces here.
	return r.sendCtx.Err()
}

func (r *poolRun) dispatch(p *WorkerPool, in <-chan Request, out chan<- Result) error {
	for {
		select {
		case <-r.sendCtx.Done():
			return r.finish(r.sendCtx.Err())
		case req, ok := <-in:
			if !ok {
				return r.finish(nil)
			}
			if err := r.launch(p, req, out); err != nil {
				return r.finish(err)
			}
		}
	}
}

func (r *poolRun) launch(p *WorkerPool, req Request, out chan<- Result) error {
	if err := r.sendCtx.Err(); err != nil {
		return err
	}

	select {
	case r.slots <- struct{}{}:
	default:
		return fmt.Errorf("%w: %d", ErrInFlightCapExceeded, cap(r.slots))
	}

	p.inFlight.Add(1)
	r.wg.Add(1)

	go func() {
		defer r.wg.Done()

		release := func() {
			p.inFlight.Add(-1)
			<-r.slots
		}

		p.send(r.sendCtx, req, out, release, r.fail)
	}()

	return nil
}

// send delivers one request and releases its in-flight slot as soon as Send
// returns, before the result is handed to out, so a slow result consumer
// never holds a slot open.
func (p *WorkerPool) send(ctx context.Context, req Request, out chan<- Result, release func(), fail func(error)) {
	begunAt := time.Now()

	outcome, err := p.Sender.Send(ctx, req)
	release()

	if err != nil {
		if ctx.Err() == nil {
			fail(err)
		}
		return
	}

	if outcome.SentAt.IsZero() {
		outcome.SentAt = begunAt
	}
	if outcome.DoneAt.IsZero() {
		outcome.DoneAt = time.Now()
	}

	result := Result{
		Method:      req.Method,
		ScheduledAt: req.ScheduledAt,
		BegunAt:     begunAt,
		Outcome:     outcome,
	}

	select {
	case out <- result:
	case <-ctx.Done():
	}
}
