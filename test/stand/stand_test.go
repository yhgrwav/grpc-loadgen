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

package stand_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/test/stand"
)

// The stand is the source of truth for every measurement test built on it, so
// it is checked first: a stand that does not do what it was told would make
// those tests agree with a wrong report.

// ceiling bounds every wait in this file. A test must fail on a regression,
// not hang until the CI timeout.
const ceiling = 5 * time.Second

func dial(t *testing.T, s *stand.Stand) grpc_health_v1.HealthClient {
	t.Helper()

	conn, err := grpc.NewClient(s.Target(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		s.DialOption(),
	)
	if err != nil {
		t.Fatalf("dial the stand: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return grpc_health_v1.NewHealthClient(conn)
}

// start brings a stand up and takes it down with the test.
func start(t *testing.T, answer stand.Answer) *stand.Stand {
	t.Helper()

	s := stand.Start(answer)
	t.Cleanup(s.Stop)

	return s
}

// check makes one call and reports how long it took.
func check(ctx context.Context, t *testing.T, client grpc_health_v1.HealthClient) (time.Duration, error) {
	t.Helper()

	begun := time.Now()
	_, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})

	return time.Since(begun), err
}

func TestStand_ConstantDelayHoldsTheAnswer(t *testing.T) {
	const delay = 120 * time.Millisecond

	client := dial(t, start(t, stand.Constant(delay)))

	took, err := check(t.Context(), t, client)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if took < delay {
		t.Errorf("answered in %v, told to hold the answer for %v", took, delay)
	}
}

func TestStand_NegativeDelayAnswersAtOnce(t *testing.T) {
	client := dial(t, start(t, stand.Constant(-time.Second)))

	took, err := check(t.Context(), t, client)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if took > time.Second {
		t.Errorf("a negative delay held the answer for %v", took)
	}
}

func TestStand_HangingCallEndsAtTheCallersDeadline(t *testing.T) {
	const deadline = 150 * time.Millisecond

	client := dial(t, start(t, stand.Hanging()))

	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()

	took, err := check(ctx, t, client)
	if code := status.Code(err); code != codes.DeadlineExceeded {
		t.Errorf("hanging call ended with %v (%v), want DeadlineExceeded", code, err)
	}
	// Half the deadline is a floor the coarse Windows clock cannot cross: the
	// point is that the stand did not answer early, not the exact moment.
	if took < deadline/2 {
		t.Errorf("hanging call came back after %v, deadline was %v", took, deadline)
	}
}

func TestStand_StopReleasesAHangingCall(t *testing.T) {
	s := stand.Start(stand.Hanging())
	client := dial(t, s)

	done := make(chan error, 1)
	go func() {
		_, err := check(t.Context(), t, client)
		done <- err
	}()

	// The call is on its way; Stop must release it even though the caller set
	// no deadline of its own.
	s.Stop()

	select {
	case <-done:
	case <-time.After(ceiling):
		t.Fatalf("a hanging call was still held %v after Stop", ceiling)
	}
}

func TestStand_FailEveryThirdCallFailsExactlyEveryThird(t *testing.T) {
	client := dial(t, start(t, stand.FailEvery(3, codes.ResourceExhausted, 0)))

	for i := 1; i <= 9; i++ {
		_, err := check(t.Context(), t, client)
		wantFail := i%3 == 0

		if (err != nil) != wantFail {
			t.Errorf("call %d: err %v, want failure: %v", i, err, wantFail)
		}
		if wantFail && status.Code(err) != codes.ResourceExhausted {
			t.Errorf("call %d failed with %v, want ResourceExhausted", i, status.Code(err))
		}
	}
}

func TestStand_FailEveryFirstCallFailsEveryCall(t *testing.T) {
	client := dial(t, start(t, stand.FailEvery(1, codes.Internal, 0)))

	for i := 1; i <= 3; i++ {
		_, err := check(t.Context(), t, client)
		if status.Code(err) != codes.Internal {
			t.Errorf("call %d ended with %v, want Internal", i, status.Code(err))
		}
	}
}

func TestStand_FailEveryBelowOneFailsNothing(t *testing.T) {
	client := dial(t, start(t, stand.FailEvery(0, codes.Internal, 0)))

	for i := 1; i <= 3; i++ {
		if _, err := check(t.Context(), t, client); err != nil {
			t.Errorf("call %d failed with %v, want no failures at all", i, err)
		}
	}
}

func TestStand_FailEveryRarerThanTheRunFailsNothing(t *testing.T) {
	client := dial(t, start(t, stand.FailEvery(100, codes.Internal, 0)))

	for i := 1; i <= 3; i++ {
		if _, err := check(t.Context(), t, client); err != nil {
			t.Errorf("call %d failed with %v, want no failures at all", i, err)
		}
	}
}

func TestStand_SlowingHoldsBackOnlyTheCallsAfterTheSwitch(t *testing.T) {
	const slow = 200 * time.Millisecond

	client := dial(t, start(t, stand.Slowing(1, 0, slow)))

	fastTook, err := check(t.Context(), t, client)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	slowTook, err := check(t.Context(), t, client)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if fastTook >= slow {
		t.Errorf("the call before the switch took %v, the switch is at %v", fastTook, slow)
	}
	if slowTook < slow {
		t.Errorf("the call after the switch took %v, told to hold it for %v", slowTook, slow)
	}
}

func TestStand_ArrivalsAreEmptyWithoutCalls(t *testing.T) {
	if got := start(t, nil).Arrivals(); len(got) != 0 {
		t.Errorf("a stand nobody called reports %d arrivals", len(got))
	}
}

func TestStand_ArrivalsAreRecordedInOrder(t *testing.T) {
	s := start(t, nil)
	client := dial(t, s)

	for i := 1; i <= 3; i++ {
		if _, err := check(t.Context(), t, client); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	arrivals := s.Arrivals()
	if len(arrivals) != 3 {
		t.Fatalf("recorded %d arrivals for 3 calls", len(arrivals))
	}
	if !sort.SliceIsSorted(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) }) {
		t.Errorf("arrivals are out of order: %v", arrivals)
	}
}

func TestStand_ConcurrentCallsGetDistinctNumbers(t *testing.T) {
	const calls = 50

	var (
		mu    sync.Mutex
		seen  []int
		count = func(c stand.Call) stand.Behavior {
			mu.Lock()
			defer mu.Unlock()

			seen = append(seen, c.N)

			return stand.Behavior{}
		}
	)

	client := dial(t, start(t, count))

	var wg sync.WaitGroup
	for range calls {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := check(t.Context(), t, client); err != nil {
				t.Errorf("concurrent call: %v", err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	sort.Ints(seen)

	if len(seen) != calls {
		t.Fatalf("%d calls got %d numbers", calls, len(seen))
	}
	for i, n := range seen {
		if n != i+1 {
			t.Fatalf("call numbers are not 1..%d: %v", calls, seen)
		}
	}
}
