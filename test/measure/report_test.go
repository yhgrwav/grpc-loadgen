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
	"github.com/yhgrwav/leettest/pkg/metrics"
	"github.com/yhgrwav/leettest/test/stand"
)

// ceiling bounds a run that should take about a second. A regression must
// fail the test, not eat the CI timeout.
const ceiling = 30 * time.Second

// slack is how much longer than the stand's delay a call may take before the
// number stops being believable. It covers scheduling on a loaded runner, not
// a measurement error: the tool is expected to report the delay it was given
// plus the little the generator itself costs.
const slack = 100 * time.Millisecond

// load describes a run at a constant rate against one method.
func load(method string, rps int, duration, timeout time.Duration) engine.Call {
	return engine.Call{
		Method:  method,
		Timeout: timeout,
		Stages:  []engine.Stage{{StartRPS: rps, TargetRPS: rps, Duration: duration}},
	}
}

// checkArrivals asserts that the stand saw the run the plan asked for and
// returns how many calls it saw. Everything else is checked against that
// number rather than against the plan: the stand's record is what actually
// happened, and one call more or less falls out of where the last one lands
// relative to the stage's end.
func checkArrivals(t *testing.T, arrivals []time.Time, rps int, duration time.Duration) int {
	t.Helper()

	planned := int(duration * time.Duration(rps) / time.Second)

	if got := len(arrivals); got < planned-1 || got > planned+1 {
		t.Fatalf("the stand saw %d calls, %d rps for %v holds %d", got, rps, duration, planned)
	}

	return len(arrivals)
}

// run loads the stand through the whole stack and returns what the report
// says about the single method that was called.
func run(t *testing.T, s *stand.Stand, call engine.Call, maxInFlight int) (engine.Report, engine.MethodReport) {
	t.Helper()

	sender := grpcsender.New(grpcsender.Options{
		Target:      s.Target(),
		DialOptions: []grpc.DialOption{s.DialOption()},
	})
	if err := sender.Connect(t.Context()); err != nil {
		t.Fatalf("connect to the stand: %v", err)
	}

	t.Cleanup(func() { _ = sender.Close() })

	// The tests size the cap by rps × timeout; the engine's own reserve, the
	// edge slot and room for late release, goes on top.
	rps := call.Stages[0].TargetRPS
	reserve := 1 + int((time.Duration(rps)*engine.ReleaseMargin+time.Second-1)/time.Second)

	eng, err := engine.New(engine.Options{
		Calls:       []engine.Call{call},
		Sender:      sender,
		MaxInFlight: maxInFlight + reserve,
	})
	if err != nil {
		t.Fatalf("build the engine: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), ceiling)
	defer cancel()

	if err := eng.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	report := eng.Report()
	if report.Incomplete {
		t.Fatalf("the run did not finish its plan; it had %v", ceiling)
	}
	if len(report.Methods) != 1 {
		t.Fatalf("report covers %d methods, one was called", len(report.Methods))
	}

	return report, report.Methods[0]
}

// checkCounts asserts what every run must satisfy whatever the stand does: no
// impossible latency reached the report, and nothing was lost between the
// calls that were made and the calls that were counted.
func checkCounts(t *testing.T, method engine.MethodReport, sent int) {
	t.Helper()

	if method.Sent != sent {
		t.Errorf("report counts %d calls, the plan holds %d", method.Sent, sent)
	}
	if method.Invalid != 0 {
		t.Errorf("%d observations were rejected as impossible", method.Invalid)
	}
	if method.Latencies+method.Refusal.Count+method.Unanswered != sent {
		t.Errorf("%d calls have a service time, %d a refusal time and %d never reached the target, for %d calls",
			method.Latencies, method.Refusal.Count, method.Unanswered, sent)
	}
}

// arrivalsWithin counts the calls the stand saw in the window [from, to) after
// its first arrival.
func arrivalsWithin(arrivals []time.Time, from, to time.Duration) int {
	if len(arrivals) == 0 {
		return 0
	}

	first := arrivals[0]

	count := 0
	for _, at := range arrivals {
		since := at.Sub(first)
		if since >= from && since < to {
			count++
		}
	}

	return count
}

// --- the target is slow -------------------------------------------------

// TestReport_ConstantDelayIsReportedAsLatency is the coordinated omission
// check in its plainest form: the stand holds every answer for a known time,
// so every percentile must be that time. A tool that reports the time it spent
// on a call rather than the time the call took shows milliseconds here.
func TestReport_ConstantDelayIsReportedAsLatency(t *testing.T) {
	const (
		delay    = 200 * time.Millisecond
		rps      = 100
		duration = time.Second
	)

	target := stand.Start(stand.Constant(delay))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	if method.Failed != 0 {
		t.Errorf("%d calls failed against a stand that answers every one", method.Failed)
	}
	if method.Censored != 0 {
		t.Errorf("%d answered calls were recorded as cut short", method.Censored)
	}

	for _, p := range []struct {
		name string
		q    metrics.Quantile
	}{
		{"p50", method.P50},
		{"p90", method.P90},
		{"p99", method.P99},
	} {
		if !p.q.Defined {
			t.Errorf("%s is undefined although %d calls were answered", p.name, sent)

			continue
		}
		if !p.q.Exact {
			t.Errorf("%s is marked inexact although no call was cut short", p.name)
		}
		if p.q.Value < delay {
			t.Errorf("%s is %v, below the %v the stand held every answer for", p.name, p.q.Value, delay)
		}
		if p.q.Value > delay+slack {
			t.Errorf("%s is %v, the stand held every answer for %v", p.name, p.q.Value, delay)
		}
	}
}

// TestReport_TheGeneratorKeepsSendingWhileTheTargetIsSlow states the open
// model against the stand's own record: waiting for an answer before sending
// the next call would stretch the run and leave the second half of the window
// almost empty.
func TestReport_TheGeneratorKeepsSendingWhileTheTargetIsSlow(t *testing.T) {
	const (
		delay    = 400 * time.Millisecond
		rps      = 100
		duration = time.Second
		window   = 500 * time.Millisecond
	)

	target := stand.Start(stand.Constant(delay))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)

	arrivals := target.Arrivals()
	checkCounts(t, method, checkArrivals(t, arrivals, rps, duration))

	// A generator that waited for each answer would have sent about one call
	// per 400ms; the plan is one per 10ms.
	const least = 40

	if got := arrivalsWithin(arrivals, 0, window); got < least {
		t.Errorf("only %d calls arrived in the first %v, the plan holds %d", got, window, least)
	}
}

// --- the target stops answering -----------------------------------------

// TestReport_HangLongerThanTheTimeoutIsCensoredAtTheTimeout pins what a
// timeout is worth: the call is known to have lasted at least its timeout and
// no more than that is claimed. Recording it as a fast failure, or leaving it
// out, would pull the whole distribution down.
func TestReport_HangLongerThanTheTimeoutIsCensoredAtTheTimeout(t *testing.T) {
	const (
		timeout  = 200 * time.Millisecond
		rps      = 50
		duration = 400 * time.Millisecond
	)

	target := stand.Start(stand.Hanging())
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, timeout), 4*rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	if method.Failed != sent {
		t.Errorf("%d calls of %d are reported as failures against a stand that never answers",
			method.Failed, sent)
	}
	if method.Censored != sent {
		t.Errorf("%d observations of %d are marked as cut short", method.Censored, sent)
	}
	if method.Unanswered != 0 {
		t.Errorf("%d calls are counted as never delivered; every one reached the stand", method.Unanswered)
	}
	if !method.P50.Defined {
		t.Fatalf("p50 is undefined although %d calls were observed", sent)
	}
	if method.P50.Exact {
		t.Errorf("p50 is reported as exact although every call was cut off at its timeout")
	}
	if method.P50.Value < timeout {
		t.Errorf("p50 is %v, every call was watched until its %v timeout", method.P50.Value, timeout)
	}
	// The bound is the timeout itself; only histogram precision may push the
	// reported value a hair above it.
	if method.P50.Value > timeout+timeout/100 {
		t.Errorf("p50 is %v, more than the %v the calls were watched for", method.P50.Value, timeout)
	}
}

// --- the target fails on a schedule -------------------------------------

// TestReport_FailuresOnAScheduleAreCountedExactly leaves no room for rounding:
// the stand fails every third call, so the report must show exactly a third of
// them as failures and still keep their latency.
func TestReport_FailuresOnAScheduleAreCountedExactly(t *testing.T) {
	const (
		every    = 3
		delay    = 20 * time.Millisecond
		rps      = 60
		duration = time.Second
	)

	target := stand.Start(stand.FailEvery(every, codes.ResourceExhausted, delay))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	if want := sent / every; method.Failed != want {
		t.Errorf("%d calls failed, the stand was told to fail %d of %d", method.Failed, want, sent)
	}
	// A refusal that came back from the target is an observation, but of how
	// long it took to refuse, not to serve: it has its own distribution.
	if want := sent / every; method.Refusal.Count != want || method.Latencies != sent-want {
		t.Errorf("%d refusal times and %d service times, want %d and %d",
			method.Refusal.Count, method.Latencies, want, sent-want)
	}
	if method.Censored != 0 {
		t.Errorf("%d answered calls were recorded as cut short", method.Censored)
	}
}

// --- the target changes during the run ----------------------------------

// TestReport_PercentilesFollowTheTailNotTheAverage sets two service times in
// one run. The median belongs to the fast part and the tail to the slow one; a
// report that averaged them would put both in between, where nothing happened.
func TestReport_PercentilesFollowTheTailNotTheAverage(t *testing.T) {
	const (
		fast     = 50 * time.Millisecond
		slow     = 400 * time.Millisecond
		rps      = 60
		duration = time.Second
		// The switch leaves two thirds of the run fast, so the median is in the
		// fast part and the 90th percentile in the slow one.
		fastCalls = 40
		// The mean of this run is (40×50ms + 20×400ms)/60 ≈ 167ms, and not one
		// call was answered anywhere near it. The median is held below that on
		// purpose: the general slack would let an averaged report through.
		mean      = 167 * time.Millisecond
		fastSlack = 100 * time.Millisecond
	)

	target := stand.Start(stand.Slowing(fastCalls, fast, slow))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)
	sent := checkArrivals(t, target.Arrivals(), rps, duration)

	checkCounts(t, method, sent)

	if method.Failed != 0 {
		t.Errorf("%d calls failed against a stand that answers every one", method.Failed)
	}
	if method.P50.Value < fast {
		t.Errorf("p50 is %v, the fastest the stand ever answered is %v", method.P50.Value, fast)
	}
	if method.P50.Value > fast+fastSlack {
		t.Errorf("p50 is %v, the first %d calls of %d were answered in %v",
			method.P50.Value, fastCalls, sent, fast)
	}
	if method.P50.Value >= mean {
		t.Errorf("p50 is %v, at or past the run's average of %v; nothing was answered there",
			method.P50.Value, mean)
	}
	if method.P90.Value <= mean {
		t.Errorf("p90 is %v, at or below the run's average of %v; nothing was answered there",
			method.P90.Value, mean)
	}
	if method.P90.Value < slow {
		t.Errorf("p90 is %v, the last %d calls of %d were held for %v",
			method.P90.Value, sent-fastCalls, sent, slow)
	}
	if method.P90.Value > slow+slack {
		t.Errorf("p90 is %v, the stand never held an answer longer than %v", method.P90.Value, slow)
	}
}

// --- the generator's own pace -------------------------------------------

// TestReport_CallsAreSpreadEvenlyOverTheRun checks the rate against the
// stand's clock rather than our own: a second's worth of calls fired at the
// top of each second would produce the same count and a different load.
func TestReport_CallsAreSpreadEvenlyOverTheRun(t *testing.T) {
	const (
		rps      = 200
		duration = time.Second
		window   = 100 * time.Millisecond
		// A window holds a tenth of the run; half that to one and a half times
		// it leaves room for timer jitter and rules out a burst.
		least = rps / 10 / 2
		most  = rps / 10 * 3 / 2
	)

	target := stand.Start(nil)
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), rps, duration, time.Second), rps)

	arrivals := target.Arrivals()
	checkCounts(t, method, checkArrivals(t, arrivals, rps, duration))

	for w := range int(duration / window) {
		from := time.Duration(w) * window
		got := arrivalsWithin(arrivals, from, from+window)

		if got < least || got > most {
			t.Errorf("%d calls arrived in the window at %v, a tenth of the run is %d..%d",
				got, from, least, most)
		}
	}
}

// --- the target stops and resumes ---------------------------------------

// freeze is the stage 0a criterion: the target stops answering for a second in
// the middle of the run and then lets every held call go. The report must show
// about a second, not the few milliseconds each call took once released.
const (
	freezeFrom   = time.Second
	freezeLength = time.Second
	freezeDelay  = 5 * time.Millisecond
	freezeRPS    = 100
	freezeRun    = 3 * time.Second
	// A call scheduled at the start of the stop waited the whole second; the
	// top percent of three hundred calls all came in within its first 100ms.
	freezeTop = freezeLength * 9 / 10
)

// TestReport_AStopInTheMiddleIsReportedAsItsLength keeps the path simple: every
// call is sent on time and held by the target, so the stop is visible in the
// target's own service time.
func TestReport_AStopInTheMiddleIsReportedAsItsLength(t *testing.T) {
	target := stand.Start(stand.Frozen(freezeFrom, freezeLength, freezeDelay))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), freezeRPS, freezeRun, 3*time.Second), 4*freezeRPS)
	arrivals := target.Arrivals()
	sent := checkArrivals(t, arrivals, freezeRPS, freezeRun)

	checkCounts(t, method, sent)
	checkFreeze(t, method, freezeLength)

	// p99 alone would pass a closed-model generator: the few calls it sent
	// before waiting also hang for the second. The open model is what keeps
	// sending while the target is silent, a second's worth of calls. A generator
	// that waits for answers sends one per worker; 80 leaves room for timer
	// jitter and rules out any pool smaller than that.
	const least = freezeRPS * 8 / 10
	if got := arrivalsWithin(arrivals, freezeFrom, freezeFrom+freezeLength); got < least {
		t.Errorf("%d calls reached the stand during the stop, the plan sends %d; the generator stopped sending while the target was silent",
			got, freezeRPS)
	}
}

// TestReport_AStopBehindTheStreamQuotaIsReportedAsItsLength is where
// coordinated omission lives. The stand allows two streams, so during the stop
// the first two calls occupy them and every later one waits inside the
// generator, not at the target. Once released, each is sent and answered in
// milliseconds. Only latency counted from the scheduled moment sees the second
// they spent waiting to be sent; counted from the send, p90 would be ~5ms.
func TestReport_AStopBehindTheStreamQuotaIsReportedAsItsLength(t *testing.T) {
	const (
		streams = 2
		// Two thirds of the run is fast, a third is the stop, and latency in
		// the stop falls from a second to zero: the 90th percentile is the call
		// scheduled 300ms into it, ~700ms. An omitting tool shows ~5ms.
		p90Floor = freezeLength / 2
	)

	target := stand.StartWith(stand.Frozen(freezeFrom, freezeLength, freezeDelay), grpc.MaxConcurrentStreams(streams))
	t.Cleanup(target.Stop)

	_, method := run(t, target, load(target.Method(), freezeRPS, freezeRun, 3*time.Second), 4*freezeRPS)

	arrivals := target.Arrivals()
	checkCounts(t, method, checkArrivals(t, arrivals, freezeRPS, freezeRun))

	// Proof the path under test was taken: while the target held its two
	// streams, nothing else reached it.
	if got := arrivalsWithin(arrivals, freezeFrom, freezeFrom+freezeLength); got > streams {
		t.Fatalf("%d calls reached the stand during the stop, the quota lets through %d", got, streams)
	}

	// Released calls then drain through the two streams: about a hundred of
	// them at 5ms each over two streams adds a quarter second to the longest
	// wait. Counting the stop twice, the nearest wrong answer above, is 2s.
	drain := time.Duration(freezeRPS) * freezeLength / time.Second * freezeDelay / streams
	checkFreeze(t, method, freezeLength+drain)

	if !method.P90.Defined || method.P90.Value < p90Floor {
		t.Errorf("p90 is %v, calls waiting for a stream through the stop must count it; want at least %v",
			method.P90.Value, p90Floor)
	}
}

// checkFreeze asserts what both stops must show: the tail is the stop, and the
// fast two thirds of the run are untouched by it.
func checkFreeze(t *testing.T, method engine.MethodReport, longest time.Duration) {
	t.Helper()

	if method.Failed != 0 {
		t.Errorf("%d calls failed; the stand answered every one after the stop", method.Failed)
	}
	if !method.P99.Defined {
		t.Fatal("p99 is undefined although every call was answered")
	}
	if method.P99.Value < freezeTop {
		t.Errorf("p99 is %v, the stand held calls for %v; a report of milliseconds left the stop out",
			method.P99.Value, freezeLength)
	}
	if method.P99.Value > longest+slack {
		t.Errorf("p99 is %v, no call waited longer than %v", method.P99.Value, longest)
	}
	if method.P50.Value > freezeDelay+slack {
		t.Errorf("p50 is %v, two thirds of the calls were answered in %v", method.P50.Value, freezeDelay)
	}
}
