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
	ErrNoCalls     = errors.New("engine has no calls")
	ErrFakeFailure = errors.New("fake sender failure")
)

type Call struct {
	Method       string
	Payload      []byte
	Timeout      time.Duration
	Stages       []Stage
	KeepResponse bool
}

type Options struct {
	Calls       []Call
	Sender      Sender
	MaxInFlight int
	Warmup      time.Duration
}

type Engine struct {
	opts  Options
	stats *Stats
	pool  *WorkerPool

	stopOnce sync.Once
	stopped  chan struct{}
	// incomplete is set when the run ended before its plan: stopped or aborted.
	incomplete atomic.Bool
}

// CheckOptions validates everything about the calls and limits that New does,
// without a sender. A caller can reject a bad config before paying for a
// connection, and build the engine once the request bodies are ready.
func CheckOptions(opts Options) error {
	if len(opts.Calls) == 0 {
		return ErrNoCalls
	}
	if opts.MaxInFlight < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidInFlightCap, opts.MaxInFlight)
	}

	return checkInFlightBudget(opts.Calls, opts.MaxInFlight)
}

func New(opts Options) (*Engine, error) {
	if opts.Sender == nil {
		return nil, ErrNoSender
	}
	if err := CheckOptions(opts); err != nil {
		return nil, err
	}

	return &Engine{
		opts:    opts,
		stats:   NewStats(),
		pool:    NewWorkerPool(opts.Sender, opts.MaxInFlight),
		stopped: make(chan struct{}),
	}, nil
}

// Stop ends the run gently: nothing new is scheduled, and calls in flight run
// to their own deadline and are recorded as usual. Run then returns nil, but
// the report is marked incomplete. Cancelling Run's context aborts instead.
// Safe to call at any time and more than once.
func (e *Engine) Stop() {
	e.stopOnce.Do(func() { close(e.stopped) })
}

func (e *Engine) Snapshot() Snapshot {
	snapshot := e.stats.Snapshot()
	snapshot.InFlight = e.pool.inFlightCount()
	snapshot.Total = e.plannedDuration()

	targets := e.targetRates()
	for i, method := range snapshot.Methods {
		snapshot.Methods[i].TargetRPS = targets[method.Method]
	}

	return snapshot
}

func (e *Engine) targetRates() map[string]int {
	rates := make(map[string]int, len(e.opts.Calls))

	for _, call := range e.opts.Calls {
		for _, stage := range call.Stages {
			if stage.TargetRPS > rates[call.Method] {
				rates[call.Method] = stage.TargetRPS
			}
		}
	}

	return rates
}

// Calls returns the calls this engine was built for.
func (e *Engine) Calls() []Call {
	return e.opts.Calls
}

func (e *Engine) plannedDuration() time.Duration {
	var longest time.Duration

	for _, call := range e.opts.Calls {
		var total time.Duration
		for _, stage := range call.Stages {
			total += stage.Duration
		}
		if total > longest {
			longest = total
		}
	}

	return longest
}

func (e *Engine) Report() Report {
	report := e.stats.Report()
	report.Incomplete = e.incomplete.Load()

	return report
}

// Run executes the plan once; an Engine is not reused. Cancelling ctx aborts
// the run, Stop ends it gently.
func (e *Engine) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	requests := make(chan Request, e.opts.MaxInFlight)
	results := make(chan Result, e.opts.MaxInFlight)

	e.stats.Start(time.Now(), e.opts.Warmup)

	scheduleCtx, stopScheduling := context.WithCancel(runCtx)
	defer stopScheduling()

	go func() {
		select {
		case <-e.stopped:
			stopScheduling()
		case <-scheduleCtx.Done():
		}
	}()

	var (
		schedulers  sync.WaitGroup
		scheduleMu  sync.Mutex
		scheduleErr error
	)

	for _, call := range e.opts.Calls {
		schedulers.Add(1)

		go func() {
			defer schedulers.Done()

			err := NewScheduler(call).Run(scheduleCtx, requests)
			if err != nil && ctx.Err() == nil && isStopped(e.stopped) {
				// Stopped by Stop, not by a failure: the plan was cut short.
				e.incomplete.Store(true)
				return
			}
			if err != nil {
				scheduleMu.Lock()
				if scheduleErr == nil {
					scheduleErr = fmt.Errorf("%s: %w", call.Method, err)
				}
				scheduleMu.Unlock()
			}
		}()
	}

	go func() {
		schedulers.Wait()
		close(requests)
	}()

	var collector sync.WaitGroup

	collector.Add(1)
	go func() {
		defer collector.Done()

		for result := range results {
			e.stats.Record(result)
		}
	}()

	sendErr := e.pool.Run(runCtx, requests, results)
	cancel()

	close(results)
	collector.Wait()

	e.stats.Finish(time.Now())

	if ctx.Err() != nil {
		e.incomplete.Store(true)
	}

	if sendErr != nil {
		return sendErr
	}

	scheduleMu.Lock()
	defer scheduleMu.Unlock()

	return scheduleErr
}

func isStopped(stopped <-chan struct{}) bool {
	select {
	case <-stopped:
		return true
	default:
		return false
	}
}
