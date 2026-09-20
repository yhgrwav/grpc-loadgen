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
}

func New(opts Options) (*Engine, error) {
	if len(opts.Calls) == 0 {
		return nil, ErrNoCalls
	}
	if opts.Sender == nil {
		return nil, ErrNoSender
	}
	if opts.MaxInFlight < 1 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidInFlightCap, opts.MaxInFlight)
	}

	return &Engine{
		opts:  opts,
		stats: NewStats(),
		pool:  NewWorkerPool(opts.Sender, opts.MaxInFlight),
	}, nil
}

func (e *Engine) Snapshot() Snapshot {
	snapshot := e.stats.Snapshot()
	snapshot.InFlight = e.pool.InFlight()
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
	return e.stats.Report()
}

func (e *Engine) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	requests := make(chan Request, e.opts.MaxInFlight)
	results := make(chan Result, e.opts.MaxInFlight)

	e.stats.Start(time.Now(), e.opts.Warmup)

	var (
		schedulers  sync.WaitGroup
		scheduleMu  sync.Mutex
		scheduleErr error
	)

	for _, call := range e.opts.Calls {
		schedulers.Add(1)

		go func() {
			defer schedulers.Done()

			if err := NewScheduler(call).Run(runCtx, requests); err != nil {
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

	if sendErr != nil {
		return sendErr
	}

	scheduleMu.Lock()
	defer scheduleMu.Unlock()

	return scheduleErr
}
