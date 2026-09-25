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
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/grpcsender"
	"github.com/yhgrwav/leettest/test/stand"
)

// The ledger run: sent 1250 in the report, 1500 at the target, the warmup's 250
// sent and counted nowhere. Sent plus the warm-up line is every call the stand
// got, in a run with failures too: both count by the same rule.
func TestReport_SentAndWarmupAreEveryCallTheStandGot(t *testing.T) {
	const (
		rps      = 50
		warmup   = 500 * time.Millisecond
		duration = 2 * time.Second
	)

	target := stand.Start(stand.FailEvery(4, codes.Unavailable, 5*time.Millisecond))
	t.Cleanup(target.Stop)

	sender := grpcsender.New(grpcsender.Options{Target: target.Target(), DialOptions: []grpc.DialOption{target.DialOption()}})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	eng, err := engine.New(engine.Options{
		Calls:       []engine.Call{load(target.Method(), rps, duration, time.Second)},
		Sender:      sender,
		MaxInFlight: rps * 2,
		Warmup:      warmup,
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()
	if err := eng.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	report := eng.Report()
	checkNoSenderDefects(t, report)
	got := checkArrivals(t, target.Arrivals(), rps, duration)

	if report.WarmupSent == 0 {
		t.Fatalf("no warm-up calls counted in a run with a %v warmup", warmup)
	}
	if report.Sent+report.WarmupSent != got {
		t.Errorf("sent %d + warm-up %d = %d, the stand got %d", report.Sent, report.WarmupSent, report.Sent+report.WarmupSent, got)
	}
	if report.Failed+report.WarmupFailed != got/4 {
		t.Errorf("failed %d + warm-up failed %d, the stand failed every 4th of %d: %d",
			report.Failed, report.WarmupFailed, got, got/4)
	}
}
