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

// InFlightCapError is how a run ended on the in-flight cap: the calls in
// flight are cut off at At, as in an abort, and recorded rather than lost.
type InFlightCapError struct {
	Cap int
	At  time.Time
}

func (e *InFlightCapError) Error() string {
	return fmt.Sprintf("%v: %d", ErrInFlightCapExceeded, e.Cap)
}

func (e *InFlightCapError) Unwrap() error { return ErrInFlightCapExceeded }

type Result struct {
	Method      string
	ScheduledAt time.Time
	BegunAt     time.Time
	// Deadline is the instant this call was to be abandoned at, carried over
	// from the request. A call that hit it is known only to have lasted at
	// least this long, and the bound comes from here rather than from DoneAt:
	// past the deadline nobody was listening, so a later DoneAt would claim
	// more than was observed. Zero when the call had no deadline.
	Deadline time.Time
	Outcome
}

// CensorThreshold reports the latency below which an abandoned call is known
// not to have finished.
func (r Result) CensorThreshold() time.Duration {
	if r.Deadline.IsZero() {
		return r.Latency()
	}

	threshold := r.Deadline.Sub(r.ScheduledAt)
	// An aborted call was watched only until the abort, which may come well
	// before its deadline.
	if r.Category == CategoryAborted {
		threshold = max(0, min(threshold, r.Latency()))
	}

	return threshold
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

// Run sends every request from in until it closes, the context is cancelled
// or a send fails fatally. Cancelling ctx aborts the calls in flight, and each
// is still delivered to out as CategoryAborted: the caller must keep reading
// out until Run returns, or Run will not return.
func (p *WorkerPool) Run(ctx context.Context, in <-chan Request, out chan<- Result) error {
	if p.Sender == nil {
		return ErrNoSender
	}
	if p.MaxInFlight < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidInFlightCap, p.MaxInFlight)
	}

	r := newPoolRun(ctx, p.MaxInFlight)
	defer r.close()

	return r.dispatch(p, in, out)
}

// poolRun tracks the state of a single Run call: the context sends are made
// under, the in-flight slots, and the first fatal error seen by any worker.
type poolRun struct {
	parent  context.Context
	sendCtx context.Context
	abort   context.CancelFunc
	// stopWatch detaches the watcher that records the abort moment.
	stopWatch func() bool
	slots     chan struct{}

	// abortedAt is the one moment the caller aborted the run, stored before
	// sendCtx is cancelled, so every call that sees the cancellation finds it.
	// Taking time.Now() in each goroutine instead would add however long the
	// cancellation took to reach it.
	abortedAt atomic.Pointer[time.Time]

	wg sync.WaitGroup

	mu       sync.Mutex
	fatalErr error
	failed   chan struct{}
}

func newPoolRun(ctx context.Context, maxInFlight int) *poolRun {
	sendCtx, abort := context.WithCancel(context.WithoutCancel(ctx))

	r := &poolRun{
		parent:  ctx,
		sendCtx: sendCtx,
		abort:   abort,
		slots:   make(chan struct{}, maxInFlight),
		failed:  make(chan struct{}),
	}
	r.stopWatch = context.AfterFunc(ctx, func() {
		now := time.Now()
		// A cap hit may have cut the calls off first: the moment stays its.
		r.abortedAt.CompareAndSwap(nil, &now)
		abort()
	})

	return r
}

func (r *poolRun) close() {
	r.stopWatch()
	r.abort()
}

// aborted reports the moment the caller aborted the run, if it did.
func (r *poolRun) aborted() (time.Time, bool) {
	at := r.abortedAt.Load()
	if at == nil {
		return time.Time{}, false
	}

	return *at, true
}

// fail records err as the run's outcome unless one was already recorded, and
// stops every in-flight and future send.
func (r *poolRun) fail(err error) {
	r.mu.Lock()
	if r.fatalErr == nil {
		r.fatalErr = err
		close(r.failed)
	}
	r.mu.Unlock()

	r.abort()
}

func (r *poolRun) finish(err error) error {
	if errors.Is(err, ErrInFlightCapExceeded) {
		// Not a failure: the results of the calls cut off still reach out.
		r.wg.Wait()

		return err
	}
	if err != nil && r.parent.Err() == nil {
		r.fail(err)
	}
	r.wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fatalErr != nil {
		return r.fatalErr
	}
	if abortErr := r.parent.Err(); abortErr != nil {
		return abortErr
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
		// Cut the calls in flight off at this moment, as an abort does: each
		// is recorded as lasting until now instead of being dropped.
		now := time.Now()
		r.abortedAt.CompareAndSwap(nil, &now)
		r.abort()

		return &InFlightCapError{Cap: cap(r.slots), At: *r.abortedAt.Load()}
	}

	p.inFlight.Add(1)
	r.wg.Add(1)

	go func() {
		defer r.wg.Done()

		release := func() {
			p.inFlight.Add(-1)
			<-r.slots
		}

		p.send(r, req, out, release)
	}()

	return nil
}

// send delivers one request and releases its in-flight slot as soon as Send
// returns, before the result is handed to out, so a slow result consumer
// never holds a slot open.
func (p *WorkerPool) send(r *poolRun, req Request, out chan<- Result, release func()) {
	begunAt := time.Now()

	outcome, err := p.Sender.Send(r.sendCtx, req)
	release()

	if err != nil {
		at, aborted := r.aborted()
		if !aborted {
			if r.sendCtx.Err() == nil {
				r.fail(err)
			}
			return
		}

		// The caller aborted the run: the call is not lost, it is known to
		// have lasted until the abort.
		// A request launched in the instant between the abort moment and the
		// cancellation would otherwise end before it began.
		if at.Before(begunAt) {
			at = begunAt
		}
		outcome = Outcome{Category: CategoryAborted, Err: err, SentAt: begunAt, DoneAt: at}
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
		Deadline:    req.Deadline,
		Outcome:     outcome,
	}

	// Delivered after an abort too: the collector reads until the pool has
	// returned. Only a fatal failure, after which nobody may read, drops it.
	select {
	case out <- result:
	case <-r.failed:
	}
}
