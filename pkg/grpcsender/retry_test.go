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
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/stats"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// attempts replays grpc-go's stats events for one call whose first attempt
// went into a connection that died before the target read it. grpc-go then
// retries transparently and reports every attempt with its own Begin and End
// (grpc v1.84.0, stream.go newAttemptLocked and csAttempt.finish).
type attempts struct {
	h    handler
	ctx  context.Context
	call *callStats
	t0   time.Time
}

func newAttempts() (*attempts, *Sender) {
	s := New(Options{})
	call := &callStats{}

	return &attempts{
		h:    handler{streams: s.streams},
		ctx:  context.WithValue(context.Background(), callKey{}, call),
		call: call,
		t0:   time.Now(),
	}, s
}

func (a *attempts) at(ms int) time.Time { return a.t0.Add(time.Duration(ms) * time.Millisecond) }

func (a *attempts) begin(ms int, retry bool) {
	a.h.HandleRPC(a.ctx, &stats.Begin{Client: true, BeginTime: a.at(ms), IsTransparentRetryAttempt: retry})
}

func (a *attempts) header() { a.h.HandleRPC(a.ctx, &stats.OutHeader{Client: true}) }

func (a *attempts) payload(ms int) {
	a.h.HandleRPC(a.ctx, &stats.OutPayload{Client: true, SentTime: a.at(ms)})
}

func (a *attempts) trailer() { a.h.HandleRPC(a.ctx, &stats.InTrailer{Client: true}) }

func (a *attempts) end(ms int) { a.h.HandleRPC(a.ctx, &stats.End{Client: true, EndTime: a.at(ms)}) }

// Ground: concurrency — a transparent retry needs a stream written into a connection that dies
// before the target reads it; no end-to-end test times a drop that exactly. CI hit it once in
// TestSend_UnsentWhileReconnectingIsBlockedOnTheConnection (go 1.25, 2026-09-23): the gauge read
// -1 after the calls returned.
func TestRetry_AnAttemptWithoutHeadersClosesNoStream(t *testing.T) {
	a, s := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(5, true)
	a.end(100)

	if n := s.OpenStreams(); n != 0 {
		t.Errorf("open streams %d after both attempts ended, want 0", n)
	}
}

// Ground: concurrency — as above. The first attempt's headers went into a dead connection the
// target never read; the call is unsent if the retry got no stream, and a timeout blamed on the
// target otherwise.
func TestRetry_TheLastAttemptDecidesWhetherTheCallWentOut(t *testing.T) {
	a, _ := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(5, true)
	a.end(100)

	if _, _, notSent := timestamps(a.call.read(), engine.CategoryTimeout); !notSent {
		t.Error("not sent = false: only the dead connection ever saw the headers")
	}
}

// Ground: concurrency — as above. The target never read the first attempt even when its body
// went out too: grpc-go retries transparently only a stream the target did not process.
func TestRetry_ABodyTheTargetNeverReadDoesNotMakeTheCallSent(t *testing.T) {
	a, _ := newAttempts()

	a.begin(0, false)
	a.header()
	a.payload(1)
	a.end(5)
	a.begin(5, true)
	a.end(100)

	if _, _, notSent := timestamps(a.call.read(), engine.CategoryTimeout); !notSent {
		t.Error("not sent = false: only the dead connection ever saw the body")
	}
}

// Ground: concurrency — as above; when the retry does go out, the call's times are the retry's.
func TestRetry_ARetryThatWentOutCarriesItsOwnTimes(t *testing.T) {
	a, s := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(5, true)
	a.header()
	a.payload(7)
	a.trailer()
	a.end(30)

	times := a.call.read()

	if !times.sentAt.Equal(a.at(7)) {
		t.Errorf("sent at %v, want the retry's %v", times.sentAt.Sub(a.t0), 7*time.Millisecond)
	}
	if !times.answered {
		t.Error("answered = false after the retry's trailer")
	}
	if n := s.OpenStreams(); n != 0 {
		t.Errorf("open streams %d, want 0", n)
	}
	if !times.begunAt.Equal(a.at(0)) {
		t.Errorf("begun at %v, want the first attempt's 0s: the call has waited since then", times.begunAt.Sub(a.t0))
	}
}

// Ground: concurrency — as above. The first attempt's time went into a connection that died, not
// into a wait for a stream: the retry's wait for one starts at the retry.
func TestRetry_TheWaitForAStreamStartsAtTheRetry(t *testing.T) {
	a, _ := newAttempts()

	a.begin(0, false)
	a.header()
	a.end(5)
	a.begin(40, true)

	if got := a.call.read().waitFrom(); !got.Equal(a.at(40)) {
		t.Errorf("wait from %v, want the retry's 40ms", got.Sub(a.t0))
	}
}
