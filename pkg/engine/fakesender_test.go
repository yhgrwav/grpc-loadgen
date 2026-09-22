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
	"testing"
	"time"
)

// Ground: contract — FakeSender is exported; goes together with -fake moving to the built-in stand.
func TestFakeSenderReportsTimeoutOnPastDeadline(t *testing.T) {
	f := FakeSender{Delay: time.Hour}
	req := Request{Deadline: time.Now().Add(-time.Millisecond)}

	outcome, err := f.Send(context.Background(), req)

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if outcome.Category != CategoryTimeout {
		t.Errorf("category = %v, want %v", outcome.Category, CategoryTimeout)
	}
	if outcome.SentAt.IsZero() || outcome.DoneAt.IsZero() {
		t.Errorf("outcome = %+v, want non-zero SentAt and DoneAt", outcome)
	}
}

// Ground: contract — FakeSender is exported; goes together with -fake moving to the built-in stand.
func TestFakeSenderReportsErrorOnCanceledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f := FakeSender{Delay: time.Hour}

	_, err := f.Send(ctx, Request{})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want %v", err, context.Canceled)
	}
}

// Ground: contract — FakeSender is exported; goes together with -fake moving to the built-in stand.
func TestFakeSenderAlwaysFails(t *testing.T) {
	f := FakeSender{FailRatio: 1}

	outcome, err := f.Send(context.Background(), Request{})

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if outcome.Category != CategoryServerFault {
		t.Errorf("category = %v, want %v", outcome.Category, CategoryServerFault)
	}
	if !errors.Is(outcome.Err, ErrFakeFailure) {
		t.Errorf("outcome.Err = %v, want %v", outcome.Err, ErrFakeFailure)
	}
}

// Ground: contract — FakeSender is exported; goes together with -fake moving to the built-in stand.
func TestFakeSenderSucceeds(t *testing.T) {
	f := FakeSender{}

	outcome, err := f.Send(context.Background(), Request{})

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if outcome.Category != CategorySuccess {
		t.Errorf("category = %v, want %v", outcome.Category, CategorySuccess)
	}
	if outcome.Err != nil {
		t.Errorf("outcome.Err = %v, want nil", outcome.Err)
	}
	if outcome.SentAt.IsZero() || outcome.DoneAt.IsZero() {
		t.Errorf("outcome = %+v, want non-zero SentAt and DoneAt", outcome)
	}
}
