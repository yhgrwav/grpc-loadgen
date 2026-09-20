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
	"time"
)

var (
	ErrNoStages      = errors.New("scheduler has no stages")
	ErrStageRPS      = errors.New("stage rps must be positive")
	ErrStageDuration = errors.New("stage duration must be positive")
	ErrRampNotYet    = errors.New("gradual ramp is not implemented yet")
)

type Request struct {
	Method string
	// Payload is encoded once before the run starts and shared by every
	// request for this method; senders must treat it as read-only.
	Payload     []byte
	ScheduledAt time.Time
	// Deadline is the absolute moment derived from ScheduledAt plus the
	// method's timeout, so a request stuck in queue does not get a fresh
	// budget. Zero means no deadline.
	Deadline time.Time
	// KeepResponse says whether to keep the response body; needed for the
	// dependency graph, always false in the MVP.
	KeepResponse bool
}

type Scheduler struct {
	Call Call
}

func NewScheduler(call Call) *Scheduler {
	return &Scheduler{Call: call}
}

func (s *Scheduler) Run(ctx context.Context, out chan<- Request) error {
	if err := s.validate(); err != nil {
		return err
	}

	stageStart := time.Now()

	for i, stage := range s.Call.Stages {
		if err := s.runStage(ctx, out, stage, stageStart); err != nil {
			return fmt.Errorf("stage %d: %w", i, err)
		}
		stageStart = stageStart.Add(stage.Duration)
	}

	return nil
}

func (s *Scheduler) validate() error {
	if len(s.Call.Stages) == 0 {
		return ErrNoStages
	}

	for i, stage := range s.Call.Stages {
		if stage.StartRPS != stage.TargetRPS {
			return fmt.Errorf("stage %d: %w", i, ErrRampNotYet)
		}
		if stage.TargetRPS < 1 {
			return fmt.Errorf("stage %d: %w: %d", i, ErrStageRPS, stage.TargetRPS)
		}
		if stage.Duration <= 0 {
			return fmt.Errorf("stage %d: %w: %s", i, ErrStageDuration, stage.Duration)
		}
	}

	return nil
}

func (s *Scheduler) runStage(ctx context.Context, out chan<- Request, stage Stage, stageStart time.Time) error {
	for i := 0; ; i++ {
		offset := time.Duration(i) * time.Second / time.Duration(stage.TargetRPS)
		if offset >= stage.Duration {
			return nil
		}

		scheduledAt := stageStart.Add(offset)

		timer := time.NewTimer(time.Until(scheduledAt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- s.newRequest(scheduledAt):
		}
	}
}

func (s *Scheduler) newRequest(scheduledAt time.Time) Request {
	req := Request{
		Method:       s.Call.Method,
		Payload:      s.Call.Payload,
		ScheduledAt:  scheduledAt,
		KeepResponse: s.Call.KeepResponse,
	}

	if s.Call.Timeout > 0 {
		req.Deadline = scheduledAt.Add(s.Call.Timeout)
	}

	return req
}
