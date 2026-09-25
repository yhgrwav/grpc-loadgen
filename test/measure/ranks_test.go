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
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/test/stand"
)

// TestReport_P95AndP99ReadTheirOwnRanks puts the switch from fast to slow
// between the 95th and the 99th percentile. The run holds 99 to 101 calls:
// p95 is rank 95 or 96, inside the first 97 fast ones, and p99 is rank 99 or
// 100, past them. A report that printed one percentile under the other's name
// shows a fast p99 or a slow p95.
func TestReport_P95AndP99ReadTheirOwnRanks(t *testing.T) {
	const (
		fast      = 50 * time.Millisecond
		slow      = 400 * time.Millisecond
		rps       = 100
		duration  = time.Second
		fastCalls = 97
	)

	target := stand.Start(stand.Slowing(fastCalls, fast, slow))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	for _, q := range []struct {
		name string
		got  time.Duration
	}{{"p50", method.P50.Value}, {"p90", method.P90.Value}, {"p95", method.P95.Value}} {
		if q.got < fast || q.got > fast+slack {
			t.Errorf("%s is %v, the first %d calls of %d were answered in %v", q.name, q.got, fastCalls, sent, fast)
		}
	}

	if method.P99.Value < slow || method.P99.Value > slow+slack {
		t.Errorf("p99 is %v, the last %d calls of %d were held for %v", method.P99.Value, sent-fastCalls, sent, slow)
	}
	if method.Max.Value < slow || method.Max.Value > slow+slack {
		t.Errorf("max is %v, the stand held the slowest answer for %v", method.Max.Value, slow)
	}
	if method.Min.Value < fast || method.Min.Value > fast+slack {
		t.Errorf("min is %v, the stand answered the fastest in %v", method.Min.Value, fast)
	}
}

// TestReport_MaxIsTheSlowestCallNotP99 holds one call longer than every
// other slow one. The run holds 199 to 201 calls: p99 is rank 198 to 199,
// among the slow ones, and max is the single slowest. With two levels max and
// p99 are the same number, and a max that read p99 would pass.
func TestReport_MaxIsTheSlowestCallNotP99(t *testing.T) {
	const (
		fast      = 20 * time.Millisecond
		slow      = 200 * time.Millisecond
		slowest   = 700 * time.Millisecond
		rps       = 200
		duration  = time.Second
		fastCalls = 194
	)

	target := stand.Start(func(c stand.Call) stand.Behavior {
		switch {
		case c.N <= fastCalls:
			return stand.Behavior{Delay: fast}
		case c.N == fastCalls+1:
			return stand.Behavior{Delay: slowest}
		default:
			return stand.Behavior{Delay: slow}
		}
	})
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), 2*rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	if method.P99.Value < slow || method.P99.Value > slow+slack {
		t.Errorf("p99 is %v, %d of %d calls were held for %v and one for %v", method.P99.Value, sent-fastCalls-1, sent, slow, slowest)
	}
	if method.Max.Value < slowest || method.Max.Value > slowest+slack {
		t.Errorf("max is %v, the stand held the slowest answer for %v", method.Max.Value, slowest)
	}
}

// TestReport_TheRunsTotalsAreWhatTheStandSaw checks the run-wide counters, not
// only the method's: the header's numbers come from them.
func TestReport_TheRunsTotalsAreWhatTheStandSaw(t *testing.T) {
	const (
		rps      = 80
		duration = time.Second
	)

	target := stand.Start(stand.FailEvery(4, codes.Unavailable, 10*time.Millisecond))
	t.Cleanup(target.Stop)

	report, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	if report.Sent != sent {
		t.Errorf("the run counts %d sent, the stand saw %d", report.Sent, sent)
	}
	if report.Sent != method.Sent {
		t.Errorf("the run counts %d sent, its only method %d", report.Sent, method.Sent)
	}
	if want := sent / 4; report.Failed != want || method.Failed != want {
		t.Errorf("failed: run %d, method %d; every 4th of %d failed, %d", report.Failed, method.Failed, sent, want)
	}
	if report.NotSent != 0 {
		t.Errorf("%d not sent against a stand that took every call", report.NotSent)
	}
}
