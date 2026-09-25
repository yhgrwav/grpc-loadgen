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

package grpcsender

import (
	"testing"
	"time"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// A deadline already past when the call reaches the sender never touches the
// network. It is still unsent for a reason, and the engine counts an unset
// reason as a defect: the generator handed the call over too late.
func TestSend_ADeadlinePastOnArrivalIsTheGenerators(t *testing.T) {
	out := sendWithin(t, dialTarget(t, &target{}), -time.Millisecond)

	if !out.NotSent || out.NotSentOn != engine.BlockedOnGenerator {
		t.Errorf("NotSent = %v on %v, want not sent on the generator", out.NotSent, out.NotSentOn)
	}
}
