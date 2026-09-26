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
	ErrNoCalls = errors.New("engine has no calls")
	// The report is per method: two calls to one would merge into a row
	// stating one call's rate and timeout for both.
	ErrDuplicateMethod = errors.New("method appears in more than one call")
	ErrFakeFailure     = errors.New("fake sender failure")
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
	// WaitFloor replaces StreamWaitFloor when set: see WaitFloorFor.
	WaitFloor time.Duration
}

type Engine struct {
	opts  Options
	stats *Stats
	pool  *WorkerPool

	// targets is each method's highest planned rate, worked out once: the
	// live view asks for it on every frame.
	targets map[string]int

	stopOnce sync.Once
	stopped  chan struct{}
	// incomplete is set when the run ended before its plan: stopped or aborted.
	incomplete atomic.Bool
	// capHit is set when the run ended on the in-flight cap.
	capHit atomic.Pointer[InFlightCapError]
	// startedAt is when Run started, for the moment of a cap hit.
	startedAt time.Time
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
	first := make(map[string]int, len(opts.Calls))
	for i, call := range opts.Calls {
		if j, ok := first[call.Method]; ok {
			return fmt.Errorf("call %d: %w: %s, as call %d", i, ErrDuplicateMethod, call.Method, j)
		}
		first[call.Method] = i
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

	e := &Engine{
		opts:    opts,
		stats:   NewStats(),
		pool:    NewWorkerPool(opts.Sender, opts.MaxInFlight),
		stopped: make(chan struct{}),
	}
	if opts.WaitFloor > 0 {
		e.stats.SetWaitFloor(opts.WaitFloor)
	}
	e.targets = e.targetRates()

	return e, nil
}

// Stop ends the run gently: nothing new is scheduled, and calls in flight run
// to their own deadline and are recorded as usual. Run then returns nil, but
// the report is marked incomplete. Cancelling Run's context aborts instead.
// Safe to call at any time and more than once.
func (e *Engine) Stop() {
	e.stopOnce.Do(func() { close(e.stopped) })
}

// Snapshot is SnapshotInto with fresh memory: for an occasional look.
func (e *Engine) Snapshot() Snapshot {
	var snapshot Snapshot
	e.SnapshotInto(&snapshot, NewLiveBuffer(), true)

	return snapshot
}

// SnapshotInto is Stats.SnapshotInto plus what the engine knows: calls in
// flight, the planned length, each method's target rate.
func (e *Engine) SnapshotInto(dst *Snapshot, buf *LiveBuffer, percentiles bool) {
	e.stats.SnapshotInto(dst, buf, percentiles)
	dst.InFlight = e.pool.inFlightCount()
	dst.Total = e.plannedDuration()

	for i := range dst.Methods {
		dst.Methods[i].TargetRPS = e.targets[dst.Methods[i].Method]
	}
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

// timelineSlack covers what a lagging generator begins past the plan. The
// drain ends within the longest timeout and an abort records the abort moment,
// so only a generator more than this far behind lands outside — and a run
// that far behind is invalid by its lag anyway.
const timelineSlack = 10 * time.Second

func (e *Engine) longestTimeout() time.Duration {
	var longest time.Duration
	for _, call := range e.opts.Calls {
		longest = max(longest, call.Timeout)
	}

	return longest
}

// Report is meant for after Run returns: it copies the per-second timelines
// under the lock every worker records through. For live data use Snapshot.
func (e *Engine) Report() Report {
	report := e.stats.Report()
	report.Incomplete = e.incomplete.Load()
	report.Planned = e.plannedDuration()
	report.StartedAt = e.startedAt

	if r, ok := e.opts.Sender.(ConnectionReporter); ok {
		if conns, known := r.Connections(); known {
			report.Connections = &conns
		}
	}

	if hit := e.capHit.Load(); hit != nil {
		report.CapHit = &CapHit{At: hit.At.Sub(e.startedAt), Unsent: 1, OverDeadline: hit.OverDeadline}
	}

	for i := range report.Methods {
		m := &report.Methods[i]
		for _, call := range e.opts.Calls {
			if call.Method != m.Method {
				continue
			}

			from := time.Duration(0)
			if m.SilentFrom != nil {
				from = time.Duration(*m.SilentFrom) * time.Second
			}
			m.Timeout = call.Timeout
			m.RPSLow, m.RPSHigh = ratesFrom(call.Stages, from)
		}
	}

	return report
}

// ratesFrom is the lowest and highest planned rate of the stages that run at
// or after from.
func ratesFrom(stages []Stage, from time.Duration) (low, high int) {
	var at time.Duration

	first := true
	for _, stage := range stages {
		end := at + stage.Duration
		at = end
		if end <= from {
			continue
		}

		lo, hi := min(stage.StartRPS, stage.TargetRPS), max(stage.StartRPS, stage.TargetRPS)
		if first {
			low, high, first = lo, hi, false

			continue
		}
		low, high = min(low, lo), max(high, hi)
	}

	return low, high
}

// Run executes the plan once; an Engine is not reused. Cancelling ctx aborts
// the run, Stop ends it gently.
func (e *Engine) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	requests := make(chan Request, e.opts.MaxInFlight)
	results := make(chan Result, e.opts.MaxInFlight)

	methods := make([]string, 0, len(e.opts.Calls))
	for _, call := range e.opts.Calls {
		methods = append(methods, call.Method)
	}
	e.stats.Reserve(e.plannedDuration()+e.longestTimeout()+timelineSlack, methods...)
	e.startedAt = time.Now()
	e.stats.Start(e.startedAt, e.opts.Warmup)
	// Nothing is scheduled past the plan, so rates never divide by more than
	// it; a stop reports an earlier moment below.
	e.stats.EndSending(e.startedAt.Add(e.plannedDuration()))

	scheduleCtx, stopScheduling := context.WithCancel(runCtx)
	defer stopScheduling()

	go func() {
		select {
		case <-e.stopped:
			e.stats.EndSending(time.Now())
			stopScheduling()
		case <-scheduleCtx.Done():
			e.stats.EndSending(time.Now())
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

	if capErr := (*InFlightCapError)(nil); errors.As(sendErr, &capErr) {
		e.capHit.Store(capErr)
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
