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

package cli

import (
	"sync"
	"time"
)

// StopStage is what a press of the stop key did.
type StopStage int

const (
	StageNone StopStage = iota
	// StageStop stops scheduling and lets the calls in flight drain.
	StageStop
	// StageAbort cuts the calls in flight off; they are recorded as aborted.
	StageAbort
	// StageExit leaves at once without a report.
	StageExit
)

// Stopper turns repeated stop requests into three stages. The third one, and
// an abort that has not finished within grace, exit without a report: the
// abort still records results and builds the report, and if either hangs the
// user must have a way out other than kill.
type Stopper struct {
	stop, abort, exit func()
	grace             time.Duration

	mu       sync.Mutex
	presses  int
	timer    *time.Timer
	finished bool
}

func NewStopper(stop, abort, exit func(), grace time.Duration) *Stopper {
	return &Stopper{stop: stop, abort: abort, exit: exit, grace: grace}
}

func (s *Stopper) Press() StopStage {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return StageNone
	}

	s.presses++
	stage := StopStage(min(s.presses, int(StageExit)))
	if stage == StageAbort {
		s.timer = time.AfterFunc(s.grace, s.exit)
	}
	s.mu.Unlock()

	switch stage {
	case StageStop:
		s.stop()
	case StageAbort:
		s.abort()
	default:
		s.exit()
	}

	return stage
}

// Finish says the run has returned and the report is out, which disarms the
// exit that guards a hung abort.
func (s *Stopper) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.finished = true
	if s.timer != nil {
		s.timer.Stop()
	}
}

// Abort is SIGTERM: it raises the stage to the abort and never past it, so an
// abort already under way still gets to print its report.
func (s *Stopper) Abort() StopStage {
	s.mu.Lock()
	if s.finished || s.presses >= int(StageAbort) {
		s.mu.Unlock()
		return StageNone
	}
	s.presses = int(StageAbort)
	s.timer = time.AfterFunc(s.grace, s.exit)
	s.mu.Unlock()

	s.abort()
	return StageAbort
}

// Stage is how far the stop has gone, by key or by signal. It does not fall
// back when the run ends: the view reads it to say what the next press does.
func (s *Stopper) Stage() StopStage {
	s.mu.Lock()
	defer s.mu.Unlock()

	return StopStage(min(s.presses, int(StageExit)))
}

// Stopping says a stop of any stage has started, by key or by signal.
func (s *Stopper) Stopping() bool {
	return s.Stage() > StageNone
}
