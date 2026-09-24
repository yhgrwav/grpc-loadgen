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

package measure

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/test/stand"
)

// TestReport_FailedCallsCarryTheCodeTheStandSent: the stand fails every 3rd
// call with RESOURCE_EXHAUSTED, and the report says so under that code.
func TestReport_FailedCallsCarryTheCodeTheStandSent(t *testing.T) {
	const (
		rps      = 60
		duration = time.Second
	)

	target := stand.Start(stand.FailEvery(3, codes.ResourceExhausted, 5*time.Millisecond))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	want := []engine.CodeCount{{Code: codes.ResourceExhausted.String(), Count: sent / 3, FromTarget: true}}
	if !slices.Equal(method.FailureCodes, want) {
		t.Errorf("failure codes %v, want %v: every 3rd of %d calls", method.FailureCodes, want, sent)
	}
}
