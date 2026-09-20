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
	ErrNoStages   = errors.New("scheduler has no stages")
	ErrInvalidRPS = errors.New("stage rps must be positive")
	ErrRampNotYet = errors.New("gradual ramp is not implemented yet")
)

type Request struct {
	ScheduledAt time.Time
}

type Scheduler struct {
	Stages []Stage
}

func NewScheduler(stages []Stage) *Scheduler {
	return &Scheduler{
		Stages: stages,
	}
}

func (s *Scheduler) Run(ctx context.Context, out chan<- Request) error {
	if len(s.Stages) == 0 {
		return ErrNoStages
	}

	stageStart := time.Now()

	for i, stage := range s.Stages {
		if err := s.runStage(ctx, out, stage, stageStart); err != nil {
			return fmt.Errorf("stage %d: %w", i, err)
		}
		stageStart = stageStart.Add(stage.Duration)
	}

	return nil
}

func (s *Scheduler) runStage(ctx context.Context, out chan<- Request, stage Stage, stageStart time.Time) error {
	if stage.StartRPS != stage.TargetRPS {
		return ErrRampNotYet
	}
	if stage.TargetRPS < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidRPS, stage.TargetRPS)
	}

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
		case out <- Request{ScheduledAt: scheduledAt}:
		}
	}
}
