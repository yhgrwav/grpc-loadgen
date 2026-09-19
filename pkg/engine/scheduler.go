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
	"time"
)

type Request struct {
	ScheduledAt time.Time
}

type Scheduler struct {
	RPS          int
	LoadDuration time.Duration
	RampUp       RampUpParams
	Warmup       WarmupParams
}

type WarmupParams struct {
	WarmupTill time.Time
}

func NewScheduler(rps int) *Scheduler {
	return &Scheduler{
		RPS: rps,
	}
}

func (s *Scheduler) Run(ctx context.Context, out chan<- Request) {
	start := time.Now()

	for i := 0; ; i++ {
		scheduledAt := start.Add(time.Duration(i) * time.Second / time.Duration(s.RPS))

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(scheduledAt)):
		}

		select {
		case <-ctx.Done():
			return
		case out <- Request{ScheduledAt: scheduledAt}:
		}
	}
}
