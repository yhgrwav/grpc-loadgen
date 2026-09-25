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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yhgrwav/leettest/pkg/metrics"
)

type Snapshot struct {
	Elapsed time.Duration
	Total   time.Duration
	Sent    int
	Failed  int
	// NotSent counts calls that timed out before going out: absent from Sent
	// and Failed, which are about the target.
	NotSent int
	// Warmup is the planned warmup; WarmupSent counts its calls that went out,
	// so a live view has something to show while Sent is still zero.
	Warmup     time.Duration
	WarmupSent int
	InFlight   int
	RPS        float64
	P50        metrics.Quantile
	P90        metrics.Quantile
	P99        metrics.Quantile
	Methods    []MethodSnapshot
}

type MethodSnapshot struct {
	Method    string
	Sent      int
	Failed    int
	RPS       float64
	TargetRPS int
	P50       metrics.Quantile
	P90       metrics.Quantile
	P99       metrics.Quantile
}

// CodeCount is how many calls failed with one transport code from one source.
type CodeCount struct {
	Code  string
	Count int
	// FromTarget is true for a code that came back over the wire, false for one
	// the client's transport set. The same code from both is two entries.
	FromTarget bool
}

type codeKey struct {
	code       string
	fromTarget bool
}

// failureCodes lists the codes that came back first, then those the client
// set, each by count, the commonest first, a tie by name; nil when nothing
// failed.
func failureCodes(codes map[codeKey]int) []CodeCount {
	if len(codes) == 0 {
		return nil
	}

	out := make([]CodeCount, 0, len(codes))
	for k, n := range codes {
		out = append(out, CodeCount{Code: k.code, Count: n, FromTarget: k.fromTarget})
	}

	slices.SortFunc(out, func(a, b CodeCount) int {
		if a.FromTarget != b.FromTarget {
			if a.FromTarget {
				return -1
			}

			return 1
		}
		if a.Count != b.Count {
			return b.Count - a.Count
		}

		return strings.Compare(a.Code, b.Code)
	})

	return out
}

type MethodReport struct {
	Method string
	Sent   int
	Failed int
	RPS    float64
	Min    metrics.Quantile
	P50    metrics.Quantile
	P90    metrics.Quantile
	P95    metrics.Quantile
	P99    metrics.Quantile
	Max    metrics.Quantile
	// P99WithoutClientWaits is P99 of the same calls with each one's
	// client-side waits taken out — start lag, the wait for a connection and
	// for a stream: the time the target had them. After a resolver wait
	// ConnWait also holds the caller's interceptors, so this can read a little
	// low.
	P99WithoutClientWaits metrics.Quantile
	// FailureCodes counts the failed calls by the transport's own code, such
	// as Unavailable, and by whether it came back over the wire or the client
	// set it: the category says whose fault a failure is, the code is what the
	// target's logs call it. A call whose transport gives no code is not listed.
	FailureCodes []CodeCount
	// The percentiles above are the service time: successes, with timeouts and
	// aborted calls as lower bounds of it. Refusals — the target answering it
	// will not serve — are a different quantity, often far faster, and are in
	// Refusal instead.
	//
	// Latencies counts the observations behind the percentiles, Censored how
	// many of them only have a lower bound, and Invalid how many were rejected
	// as impossible — a negative latency means the time arithmetic is wrong.
	Latencies int
	Censored  int
	Invalid   int
	// Unanswered counts calls that never reached the target, so they are absent
	// from the distribution rather than recorded as very fast replies.
	Unanswered int
	// CutOff counts calls that went out and got no status back: counted as
	// failed, absent from every latency, and not an answer.
	CutOff int
	// Unclassified counts calls the sender left without a category: a defect
	// of the sender, kept apart so it does not pass for an unreachable target.
	// They are absent from the distribution too.
	Unclassified int
	// The method's share of the run's totals, by the same rules: Aborted is
	// among Sent, NotSent* are out of it, Warmup* are out of all of them.
	Aborted           int
	NotSent           int
	NotSentLate       int
	NotSentStream     int
	NotSentConnection int
	WarmupSent        int
	WarmupFailed      int
	WarmupNotSent     int
	Refusal           RefusalLatency
	// Rejected is the target answering that the request itself is wrong —
	// no such method, bad argument, a message over a size limit. Such a call
	// says nothing about the load: it would fail the same way at any rate.
	Rejected RefusalLatency
	// Seconds covers the whole run, warmup included, up to the last second
	// anything happened in. Unlike the totals it keeps every call.
	Seconds []Second
	// OutsideTimeline counts calls left off Seconds because a moment of theirs
	// could not be placed on it; InvalidLag, calls begun before their schedule.
	OutsideTimeline int
	InvalidLag      int
	// TimedOut counts calls that went out and got no answer within their
	// timeout; UnsentTimedOut, timeouts of calls that never went out, which
	// say nothing about the target.
	TimedOut int
	// TimedOutAfterWait counts the TimedOut calls that went out with less
	// than half their timeout left: the target had the smaller part of it.
	// A deadline that ran out before sending is not here: that call is
	// NotSent, in UnsentTimedOut.
	TimedOutAfterWait int
	UnsentTimedOut    int
	// SilentFrom is the first second, by planned time and counting warmup,
	// from which to the end of the schedule no call got an answer: neither a
	// success nor a status from the target. Nil if there is none.
	SilentFrom *int
	// LastAnswerAt is when the last call the target answered was scheduled,
	// measured from the start of the run: where the silence begins. Nil when
	// the target answered nothing at all.
	LastAnswerAt *time.Duration
	// RPSLow and RPSHigh are the planned rates over the stages the statement
	// covers: from SilentFrom on if it is set, the whole plan otherwise.
	RPSLow, RPSHigh int
	// Timeout is the method's timeout, the T of "no answer within T".
	Timeout time.Duration
}

// CapHit is how a run stopped on the in-flight cap. In a run the budget
// accepted, only slots held past their deadline by more than the budget's
// margin fill the cap: the generator's side, not the target's.
type CapHit struct {
	// At is when the cap was hit, from the start of the run.
	At time.Duration
	// Unsent is the calls the cap refused: they were never sent.
	Unsent int
	// OverDeadline is the calls in flight at that moment whose deadline had
	// already passed.
	OverDeadline int
}

// RefusalLatency is how long the target took to refuse: server faults and
// overload, or, in Rejected, a request it would never serve. A slow refusal is worse than a fast one.
type RefusalLatency struct {
	Count int
	P50   metrics.Quantile
	P90   metrics.Quantile
	P95   metrics.Quantile
	P99   metrics.Quantile
	Max   metrics.Quantile
}

type Report struct {
	Duration time.Duration
	// Warmup is the leading span of the run whose calls are on Seconds but not
	// in the totals: a call is warmup by the moment it was scheduled for.
	Warmup time.Duration
	// WarmupSent and WarmupFailed count the warmup's calls by the rule of Sent
	// and Failed: Sent plus WarmupSent is every call that went out.
	WarmupSent   int
	WarmupFailed int
	Sent         int
	Failed       int
	// NotSent counts calls that timed out before going out: absent from Sent
	// and Failed, which are about the target.
	NotSent int
	// WarmupNotSent counts the warmup's calls that timed out before going out.
	WarmupNotSent int
	Methods       []MethodReport
	// Aborted counts calls cut off by an abort of the run. They are no fault
	// of the target, so they are not in Failed; each is censored at the abort.
	Aborted int
	// Planned is how long the schedule was meant to run; Duration how long it
	// did.
	Planned time.Duration
	// CapHit is set when the run stopped on the in-flight cap.
	CapHit *CapHit
	// StartLagP99 and StartLagMax are how late calls started against their
	// schedule. They do not see a generator late to pick up an answer.
	StartLagP99 metrics.Quantile
	StartLagMax time.Duration
	// LateCancelMax is how far past its deadline a timed-out call returned
	// on its own: how late cancellation ran.
	LateCancelMax time.Duration

	// NotSentLate, NotSentStream and NotSentConnection split NotSent by what
	// kept a call from going out: the generator's lag, a ready connection
	// with no free stream, or a connection that was not ready. They add up
	// to NotSent.
	NotSentLate       int
	NotSentStream     int
	NotSentConnection int
	// StreamWaited counts calls that went out after waiting for a stream
	// longer than StreamWaitFloor; StreamWaitP99 is over those calls.
	StreamWaited  int
	StreamWaitP99 metrics.Quantile
	// GeneratorCauseCalls, ConnectionCauseCalls and StreamCauseCalls count the
	// measured calls, sent or not, that waited over StreamWaitFloor for each
	// cause: start lag behind the schedule, a connection that was not ready, a
	// free stream. An unsent call counts only for the cause that kept it back,
	// so StreamCauseCalls is StreamWaited + NotSentStream. A sent call can
	// count for several causes. They rank the causes; they do not decide
	// whether there is a verdict.
	GeneratorCauseCalls  int
	ConnectionCauseCalls int
	StreamCauseCalls     int
	// GeneratorTailCalls, ConnectionTailCalls and StreamTailCalls count the
	// same, but only among the calls that set each method's printed p99 — at
	// or above it — plus the unsent calls kept back by the cause. They say
	// which cause made the tail, and name the verdict.
	GeneratorTailCalls  int
	ConnectionTailCalls int
	StreamTailCalls     int
	// Connections is what the sender said about its connections; nil when it
	// does not tell.
	Connections *Connections

	// RequestRejected says every measured call of some method came back as a
	// request the target will never serve: the run tested nothing about load.
	RequestRejected bool
	// Incomplete says the run ended before its plan, by Stop or by an abort.
	// Every number is honest, but it covers less than was asked for.
	Incomplete bool
}

type Stats struct {
	mu        sync.Mutex
	startedAt time.Time
	// sendingEndedAt is when the schedule stopped handing out calls: its
	// planned end or an earlier stop. Zero until reported.
	sendingEndedAt time.Time
	endedAt        time.Time
	warmup         time.Duration
	// warmupSent, warmupFailed and warmupNotSent count the warmup's calls by
	// the rule of sent, failed and notSent.
	warmupSent, warmupFailed, warmupNotSent int
	sent                                    int
	failed                                  int
	notSent                                 int
	aborted                                 int
	reserve                                 int
	byMethod                                map[string]*methodStats
	// startLag is how late calls began against their schedule, startLagMax
	// its exact maximum; lateCancelMax how far past its deadline a timeout
	// returned.
	startLag      *metrics.Latencies
	startLagMax   time.Duration
	lateCancelMax time.Duration
	// notSentLate, notSentStream and notSentConnection split notSent by reason;
	// streamWait is the wait of sent calls that waited over StreamWaitFloor.
	notSentLate       int
	notSentStream     int
	notSentConnection int
	streamWait        *metrics.Latencies
	// waited counts calls over the floor for each cause: generator,
	// connection, stream.
	waited [3]int
}

type methodStats struct {
	sent       int
	failed     int
	unanswered int
	cutOff     int
	unknown    int
	timedOut   int
	// timedOutLate counts timeouts sent with less than half the deadline left.
	timedOutLate int
	unsentOut    int
	aborted      int
	// The method's share of the run's split of unsent calls and of warmup.
	notSentLate, notSentStream, notSentConnection int
	warmupSent, warmupFailed, warmupNotSent       int
	latency                                       *metrics.Latencies
	// served is latency with each call's client-side waits taken out: start
	// lag, the wait for a connection and for a stream.
	served *metrics.Latencies
	// waited is the latency of the calls that waited over StreamWaitFloor for
	// each cause — generator, connection, stream — to count them in the tail;
	// nil until one does.
	waited   [3]*metrics.Latencies
	refusal  *metrics.Latencies
	rejected *metrics.Latencies
	timeline timeline
	// codes counts failed calls by the transport's code and its source; nil
	// until one fails.
	codes map[codeKey]int
	// lastAnswer is the latest scheduled moment among the calls the target
	// answered, as an offset from the start of the run; -1 until one is.
	lastAnswer time.Duration
}

func NewStats() *Stats {
	return &Stats{
		byMethod:   make(map[string]*methodStats),
		startLag:   metrics.NewUncensoredLatencies(),
		streamWait: metrics.NewUncensoredLatencies(),
	}
}

// Reserve fixes the span of the timeline: calls with a moment past it are
// counted in OutsideTimeline instead. The space for the named methods is
// taken here rather than on their first call, which records under the lock.
// Without Reserve the timeline is empty and every call is outside it.
func (s *Stats) Reserve(span time.Duration, methods ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reserve = int(span/time.Second) + 1
	for _, name := range methods {
		if _, ok := s.byMethod[name]; !ok {
			s.byMethod[name] = s.newMethod()
		}
	}
}

func (s *Stats) newMethod() *methodStats {
	return &methodStats{
		latency:    metrics.NewLatencies(),
		served:     metrics.NewLatencies(),
		refusal:    metrics.NewUncensoredLatencies(),
		rejected:   metrics.NewUncensoredLatencies(),
		lastAnswer: -1,
		timeline:   newTimeline(s.reserve),
	}
}

func (s *Stats) Start(at time.Time, warmup time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.startedAt = at
	s.warmup = warmup
}

// EndSending reports a moment the schedule stopped handing out calls; the
// earliest reported one counts. Rates divide by the time up to it: what comes
// after is waiting for answers, not sending.
func (s *Stats) EndSending(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sendingEndedAt.IsZero() || at.Before(s.sendingEndedAt) {
		s.sendingEndedAt = at
	}
}

func (s *Stats) Finish(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.endedAt = at
}

// Record files one finished call. Requests inside the warmup window are left
// out of the counters as much as of the distribution — only the timeline keeps
// them — because the report describes the measured part of the run, and
// counting them would skew the reported rate and hide the cold-start failures
// warmup exists to absorb.
func (s *Stats) Record(r Result) {
	s.mu.Lock()

	method, ok := s.byMethod[r.Method]
	if !ok {
		method = s.newMethod()
		s.byMethod[r.Method] = method
	}

	// The timeline keeps warmup: a target failing on the way up is exactly
	// what it should show, and the report says which seconds were warmup.
	method.timeline.record(s.startedAt, r)

	switch r.Category {
	case CategorySuccess, CategoryServerFault, CategoryOverload, CategoryClientFault:
		method.lastAnswer = max(method.lastAnswer, r.ScheduledAt.Sub(s.startedAt))
	}

	if r.ScheduledAt.Before(s.startedAt.Add(s.warmup)) {
		if r.NotSent {
			s.warmupNotSent++
			method.warmupNotSent++
		} else {
			s.warmupSent++
			method.warmupSent++
			if r.Category != CategorySuccess && r.Category != CategoryAborted {
				s.warmupFailed++
				method.warmupFailed++
			}
		}
		s.mu.Unlock()

		return
	}

	if lag := r.QueueTime(); lag >= 0 {
		s.startLag.Record(lag)
		s.startLagMax = max(s.startLagMax, lag)
	}

	if r.Category == CategoryTimeout && !r.Deadline.IsZero() {
		s.lateCancelMax = max(s.lateCancelMax, r.DoneAt.Sub(r.Deadline))
	}

	// A call that never went out tells nothing about the target: it is in
	// none of the counts or distributions that are about it.
	gen, conn, stream := r.QueueTime() > StreamWaitFloor, r.ConnWait > StreamWaitFloor, r.StreamWait > StreamWaitFloor
	if r.NotSent {
		s.notSent++
		method.unsentOut++
		// Only the cause that kept it back: its other waits are cut short.
		gen, conn, stream = false, false, false
		switch notSentCause(r) {
		case BlockedOnGenerator:
			s.notSentLate++
			method.notSentLate++
			gen = true
		case BlockedOnStream:
			s.notSentStream++
			method.notSentStream++
			stream = true
		default:
			s.notSentConnection++
			method.notSentConnection++
			conn = true
		}
		s.countWaits(gen, conn, stream)
		s.mu.Unlock()

		return
	}

	s.sent++
	failed := r.Category != CategorySuccess && r.Category != CategoryAborted
	if failed {
		s.failed++
	}
	if r.Category == CategoryAborted {
		s.aborted++
		method.aborted++
	}

	method.sent++
	if failed {
		method.failed++
		if r.Code != "" {
			if method.codes == nil {
				method.codes = make(map[codeKey]int)
			}
			method.codes[codeKey{r.Code, r.CodeFromTarget}]++
		}
	}

	if stream {
		s.streamWait.Record(r.StreamWait)
	}
	s.countWaits(gen, conn, stream)

	if r.Category == CategoryTimeout {
		method.timedOut++
		if !r.Deadline.IsZero() && r.Deadline.Sub(r.SentAt) < r.Deadline.Sub(r.ScheduledAt)/2 {
			method.timedOutLate++
		}
	}

	// A call that never reached the target has no latency to record: a refused
	// connection comes back in microseconds and would pull both the median and
	// the tail down while the target is in fact unreachable. A cut-off call has
	// no status to time either.
	unanswered := r.Category == CategoryUnknown || r.Category == CategoryUnreachable || r.Category == CategoryCutOff
	switch r.Category {
	case CategoryUnreachable:
		method.unanswered++
	case CategoryCutOff:
		method.cutOff++
	case CategoryUnknown:
		method.unknown++
	}

	var waited [3]*metrics.Latencies
	if !unanswered {
		for i, w := range [3]bool{gen, conn, stream} {
			if !w {
				continue
			}
			if method.waited[i] == nil {
				method.waited[i] = metrics.NewLatencies()
			}
			waited[i] = method.waited[i]
		}
	}

	s.mu.Unlock()

	if unanswered {
		return
	}

	// The three waits follow one another before the request goes out, so they
	// never add up past the latency; if they do, the arithmetic is wrong and
	// the difference is held at zero rather than recorded as negative.
	clientWait := max(0, r.QueueTime()) + r.ConnWait + r.StreamWait

	// An abandoned call is known only to have lasted at least as long as its
	// deadline, so it is recorded as a bound rather than as a measurement.
	if r.Category == CategoryTimeout || r.Category == CategoryAborted {
		threshold := r.CensorThreshold()
		method.latency.RecordCensored(threshold)
		method.served.RecordCensored(max(0, threshold-clientWait))
		for _, w := range waited {
			if w != nil {
				w.RecordCensored(threshold)
			}
		}

		return
	}

	if r.Category == CategorySuccess {
		for _, w := range waited {
			if w != nil {
				w.Record(r.Latency())
			}
		}
	}

	switch r.Category {
	case CategorySuccess:
		method.latency.Record(r.Latency())
		method.served.Record(max(0, r.Latency()-clientWait))
	case CategoryServerFault, CategoryOverload:
		method.refusal.Record(r.Latency())
	case CategoryClientFault:
		method.rejected.Record(r.Latency())
	}
}

// methodView is one method's counters taken under the lock together with a
// snapshot of its distribution, so percentiles can be computed without holding
// anything.
type methodView struct {
	name       string
	sent       int
	failed     int
	unanswered int
	cutOff     int
	unknown    int
	lastAnswer time.Duration
	dist       *metrics.Snapshot
	served     *metrics.Snapshot
	refusal    *metrics.Snapshot
	rejected   *metrics.Snapshot
	waited     [3]*metrics.Snapshot
	codes      []CodeCount
}

// views copies the counters under the lock and takes each distribution's
// snapshot outside it: every snapshot briefly locks its own distribution, and
// nesting those under the Stats lock would stall recording for as long as all
// methods together take to copy. Only Report uses it: it allocates, and the
// live view goes through SnapshotInto instead.
func (s *Stats) views() (elapsed, measured time.Duration, sent, failed int, out []methodView) {
	s.mu.Lock()
	elapsed, sent, failed = s.elapsed(), s.sent, s.failed

	measured = s.sendingWindow(elapsed)

	out = make([]methodView, 0, len(s.byMethod))
	sources := make([]*methodStats, 0, len(s.byMethod))

	for name, method := range s.byMethod {
		out = append(out, methodView{
			name: name, sent: method.sent, failed: method.failed, unanswered: method.unanswered, cutOff: method.cutOff,
			unknown: method.unknown, lastAnswer: method.lastAnswer, codes: failureCodes(method.codes),
		})
		sources = append(sources, method)
	}
	s.mu.Unlock()

	for i, src := range sources {
		out[i].dist = src.latency.Snapshot()
		out[i].served = src.served.Snapshot()
		out[i].refusal = src.refusal.Snapshot()
		out[i].rejected = src.rejected.Snapshot()
		for c, w := range src.waited {
			if w != nil {
				out[i].waited[c] = w.Snapshot()
			}
		}
	}

	slices.SortFunc(out, func(a, b methodView) int { return strings.Compare(a.name, b.name) })

	return elapsed, measured, sent, failed, out
}

// Snapshot is SnapshotInto with fresh memory: for an occasional look. A reader
// that looks several times a second keeps a LiveBuffer and calls SnapshotInto.
func (s *Stats) Snapshot() Snapshot {
	var snapshot Snapshot
	s.SnapshotInto(&snapshot, NewLiveBuffer(), true)

	return snapshot
}

// LiveBuffer is the memory repeated snapshots reuse, so that looking at a run
// does not feed the collector in the generator's process. It belongs to
// whoever made it: not safe for concurrent use, two readers need two.
type LiveBuffer struct {
	names   []string
	methods []*methodStats
	dists   []*metrics.Buffer
	merged  *metrics.Buffer
}

func NewLiveBuffer() *LiveBuffer {
	return &LiveBuffer{merged: metrics.NewBuffer()}
}

// SnapshotInto fills dst, reusing its memory and buf's. The counters are
// always taken. The percentiles only when asked: copying a distribution holds
// its lock while recording waits, so they are recomputed about once a second,
// not on every frame. Without them dst keeps the percentiles it had; the
// methods are always in the same order, by name.
func (s *Stats) SnapshotInto(dst *Snapshot, buf *LiveBuffer, percentiles bool) {
	s.mu.Lock()
	if len(buf.methods) != len(s.byMethod) {
		buf.track(s.byMethod)
	}

	elapsed, sent, failed := s.elapsed(), s.sent, s.failed
	dst.NotSent = s.notSent
	dst.Warmup, dst.WarmupSent = s.warmup, s.warmupSent
	measured := s.sendingWindow(elapsed)

	if len(dst.Methods) != len(buf.names) {
		dst.Methods = make([]MethodSnapshot, len(buf.names))
	}
	for i, method := range buf.methods {
		m := &dst.Methods[i]
		m.Method, m.Sent, m.Failed = buf.names[i], method.sent, method.failed
	}
	s.mu.Unlock()

	dst.Elapsed, dst.Sent, dst.Failed, dst.RPS = elapsed, sent, failed, 0
	if measured > 0 {
		dst.RPS = float64(sent) / measured.Seconds()
	}
	for i := range dst.Methods {
		m := &dst.Methods[i]
		m.RPS = 0
		if measured > 0 {
			m.RPS = float64(m.Sent) / measured.Seconds()
		}
	}

	if !percentiles {
		return
	}

	// Each distribution is copied under its own lock, never nested under the
	// Stats lock: that would stall recording for all methods at once.
	for i, method := range buf.methods {
		method.latency.CopyInto(buf.dists[i])

		m := &dst.Methods[i]
		m.P50 = buf.dists[i].Percentile(0.50)
		m.P90 = buf.dists[i].Percentile(0.90)
		m.P99 = buf.dists[i].Percentile(0.99)
	}

	// Distributions are merged rather than their percentiles averaged: the mean
	// of two p99s is not the p99 of anything.
	metrics.MergeInto(buf.merged, buf.dists...)
	dst.P50 = buf.merged.Percentile(0.50)
	dst.P90 = buf.merged.Percentile(0.90)
	dst.P99 = buf.merged.Percentile(0.99)
}

// track rebuilds the buffer for the methods there are now. It allocates, but
// only when a method appears; the planned ones are all there from Reserve.
func (b *LiveBuffer) track(byMethod map[string]*methodStats) {
	b.names = b.names[:0]
	for name := range byMethod {
		b.names = append(b.names, name)
	}
	slices.Sort(b.names)

	b.methods = b.methods[:0]
	for _, name := range b.names {
		b.methods = append(b.methods, byMethod[name])
	}

	for len(b.dists) < len(b.names) {
		b.dists = append(b.dists, metrics.NewBuffer())
	}
	b.dists = b.dists[:len(b.names)]
}

// Report copies every method's timeline under the lock Record takes: call it
// once the run is over. For live data use Snapshot.
func (s *Stats) Report() Report {
	elapsed, measured, sent, failed, views := s.views()

	// The timelines are copied only here, once a run is over, never for the
	// snapshots the interface takes several times a second.
	s.mu.Lock()
	aborted, warmup, notSent := s.aborted, s.warmup, s.notSent
	warmupSent, warmupFailed, warmupNotSent := s.warmupSent, s.warmupFailed, s.warmupNotSent
	late, stream, connection := s.notSentLate, s.notSentStream, s.notSentConnection
	waited := s.waited
	timelines := make(map[string]MethodReport, len(s.byMethod))
	for name, method := range s.byMethod {
		entry := MethodReport{
			Seconds:           method.timeline.export(),
			OutsideTimeline:   method.timeline.outside,
			InvalidLag:        method.timeline.invalidLag,
			TimedOut:          method.timedOut,
			TimedOutAfterWait: method.timedOutLate,
			UnsentTimedOut:    method.unsentOut,

			Aborted:           method.aborted,
			NotSent:           method.unsentOut,
			NotSentLate:       method.notSentLate,
			NotSentStream:     method.notSentStream,
			NotSentConnection: method.notSentConnection,
			WarmupSent:        method.warmupSent,
			WarmupFailed:      method.warmupFailed,
			WarmupNotSent:     method.warmupNotSent,
		}
		if from, ok := method.timeline.silentFrom(); ok {
			entry.SilentFrom = &from
		}
		if method.lastAnswer >= 0 {
			at := method.lastAnswer
			entry.LastAnswerAt = &at
		}
		timelines[name] = entry
	}
	startLag := s.startLag.Snapshot()
	streamWait := s.streamWait.Snapshot()
	startLagMax, lateCancelMax := s.startLagMax, s.lateCancelMax
	s.mu.Unlock()

	report := Report{
		Duration: elapsed,
		Warmup:   warmup,

		WarmupSent:    warmupSent,
		WarmupFailed:  warmupFailed,
		WarmupNotSent: warmupNotSent,
		Sent:          sent,
		Failed:        failed,
		NotSent:       notSent,
		Aborted:       aborted,

		StartLagP99:   startLag.Percentile(0.99),
		StartLagMax:   startLagMax,
		LateCancelMax: lateCancelMax,

		NotSentLate:          late,
		NotSentStream:        stream,
		NotSentConnection:    connection,
		StreamWaited:         int(streamWait.Count()),
		StreamWaitP99:        streamWait.Percentile(0.99),
		GeneratorCauseCalls:  waited[0],
		ConnectionCauseCalls: waited[1],
		StreamCauseCalls:     waited[2],
	}

	var tail [3]int
	for i := range views {
		v := &views[i]
		if v.sent > 0 && int(v.rejected.Count()) == v.sent {
			report.RequestRejected = true
		}
		entry := MethodReport{
			Method:       v.name,
			Sent:         v.sent,
			Failed:       v.failed,
			Latencies:    int(v.dist.Count()),
			Censored:     int(v.dist.CensoredCount()),
			Invalid:      int(v.dist.InvalidCount()),
			Unanswered:   v.unanswered,
			CutOff:       v.cutOff,
			Unclassified: v.unknown,
			Min:          v.dist.Percentile(0),
			P50:          v.dist.Percentile(0.50),
			P90:          v.dist.Percentile(0.90),
			P95:          v.dist.Percentile(0.95),
			P99:          v.dist.Percentile(0.99),
			Max:          v.dist.Percentile(1),

			P99WithoutClientWaits: v.served.Percentile(0.99),
			FailureCodes:          v.codes,
			Refusal:               refusalOf(v.refusal),
			Rejected:              refusalOf(v.rejected),

			Seconds:           timelines[v.name].Seconds,
			OutsideTimeline:   timelines[v.name].OutsideTimeline,
			InvalidLag:        timelines[v.name].InvalidLag,
			TimedOut:          timelines[v.name].TimedOut,
			TimedOutAfterWait: timelines[v.name].TimedOutAfterWait,
			UnsentTimedOut:    timelines[v.name].UnsentTimedOut,

			Aborted:           timelines[v.name].Aborted,
			NotSent:           timelines[v.name].NotSent,
			NotSentLate:       timelines[v.name].NotSentLate,
			NotSentStream:     timelines[v.name].NotSentStream,
			NotSentConnection: timelines[v.name].NotSentConnection,
			WarmupSent:        timelines[v.name].WarmupSent,
			WarmupFailed:      timelines[v.name].WarmupFailed,
			WarmupNotSent:     timelines[v.name].WarmupNotSent,
			SilentFrom:        timelines[v.name].SilentFrom,
			LastAnswerAt:      timelines[v.name].LastAnswerAt,
		}
		if measured > 0 {
			entry.RPS = float64(v.sent) / measured.Seconds()
		}

		// The tail is the calls at or above the printed p99: the ones that set
		// it. Calls just above p99 without the waits are not all among them.
		if entry.P99.Defined {
			for c, w := range v.waited {
				if w != nil {
					tail[c] += int(w.CountAtOrAbove(entry.P99.Value))
				}
			}
		}

		report.Methods = append(report.Methods, entry)
	}

	report.GeneratorTailCalls = tail[0] + late
	report.ConnectionTailCalls = tail[1] + connection
	report.StreamTailCalls = tail[2] + stream

	return report
}

// sendingWindow is what rates divide by: from the end of warmup to the end of
// sending, or to now while sending goes on. The counters exclude warmup and
// the drain after sending sends nothing, so either in the window would report
// a rate lower than the one driven.
func (s *Stats) sendingWindow(elapsed time.Duration) time.Duration {
	window := elapsed
	if !s.sendingEndedAt.IsZero() {
		window = min(window, s.sendingEndedAt.Sub(s.startedAt))
	}

	return max(0, window-s.warmup)
}

func refusalOf(dist *metrics.Snapshot) RefusalLatency {
	return RefusalLatency{
		Count: int(dist.Count()),
		P50:   dist.Percentile(0.50),
		P90:   dist.Percentile(0.90),
		P95:   dist.Percentile(0.95),
		P99:   dist.Percentile(0.99),
		Max:   dist.Percentile(1),
	}
}

func (s *Stats) elapsed() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	if s.endedAt.IsZero() {
		return time.Since(s.startedAt)
	}

	return s.endedAt.Sub(s.startedAt)
}

// countWaits adds a measured call to each cause it waited for. Callers hold mu.
func (s *Stats) countWaits(gen, conn, stream bool) {
	for i, w := range [3]bool{gen, conn, stream} {
		if w {
			s.waited[i]++
		}
	}
}

// StreamWaitFloor is the wait below which a call is not counted as having
// waited — for a stream, and for the generator and the connection alike. A hypothesis: a stream granted at once still takes a few
// microseconds between picking the connection and writing headers.
const StreamWaitFloor = time.Millisecond
