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
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// A call that went out and got no status back reached the other end: it was
// cut off, not unreachable. "Unreachable" says the target never saw the call;
// for a write that it may have processed, that is the wrong thing to say.
//
// Ground: contract — engine.CategoryCutOff is public.
func TestSend_WentOutNoStatusIsCutOffNotUnreachable(t *testing.T) {
	cases := []struct {
		name string
		send func(t *testing.T) engine.Outcome
		code codes.Code
		want engine.Category
	}{
		{"stream reset with INTERNAL_ERROR", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTargetOnData(t, func(fr *http2.Framer, id uint32) bool {
				_ = fr.WriteRSTStream(id, http2.ErrCodeInternal)

				return false
			}), 0)
		}, codes.Internal, engine.CategoryCutOff},
		// The other end took the request and dropped the connection.
		{"connection dropped after the request went out", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(*http2.Framer, uint32, int) {}, true), time.Second)
		}, codes.Unavailable, engine.CategoryCutOff},
		// The connection was gone before anything was written: nothing reached anyone.
		{"nothing answered", func(t *testing.T) engine.Outcome {
			return sendWithin(t, vanishedTarget(t), 0)
		}, codes.Unavailable, engine.CategoryUnreachable},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := c.send(t)
			if out.Code != c.code.String() {
				t.Fatalf("code = %q, want %q: the case no longer pins its code", out.Code, c.code)
			}
			if out.Category != c.want {
				t.Errorf("category = %v, want %v (sent at %v)", out.Category, c.want, out.SentAt)
			}
		})
	}
}

// The outcome and the code's source never contradict each other, for every
// code and every way a call can end. Unreachable and cut off mean no status came
// back, so their code is never the target's; overload and server fault are
// statuses that came back, so theirs always is. A later change to categorize
// that breaks this would put "unreachable" next to "sent by the target" again.
func TestCategorize_OutcomeAgreesWithTheCodesSource(t *testing.T) {
	sizeCut := status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5 vs. 4)")

	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		for _, size := range []bool{false, true} {
			if size && code != codes.ResourceExhausted {
				continue
			}
			err := status.Error(code, "x")
			if size {
				err = sizeCut
			}
			for _, answered := range []bool{true, false} {
				for _, wentOut := range []bool{true, false} {
					name := fmt.Sprintf("%v/answered=%v/wentOut=%v/size=%v", code, answered, wentOut, size)
					got := categorize(err, answered, wentOut)

					switch got {
					case engine.CategoryUnreachable, engine.CategoryCutOff:
						if answered {
							t.Errorf("%s: %v, but a status came back", name, got)
						}
					case engine.CategoryOverload, engine.CategoryServerFault:
						if !answered {
							t.Errorf("%s: %v, but no status came back", name, got)
						}
					}
					if got == engine.CategoryUnreachable && wentOut {
						t.Errorf("%s: unreachable, but the request went out", name)
					}
					if !answered && wentOut && code != codes.DeadlineExceeded && !size && got != engine.CategoryCutOff {
						t.Errorf("%s: %v, want cut off: went out, no status came back", name, got)
					}
				}
			}
		}
	}
}

// refusingAfterPayload refuses streams with REFUSED_STREAM once their DATA has
// arrived, so the attempt's OutPayload has fired. The first connection refuses
// its first stream; with once set it then hangs up and the listener is gone,
// so the transparent retry fails before writing anything. Without once every
// stream is refused. The record says what reached each side before the first
// refusal, so a test can check its scenario held.
func refusingAfterPayload(t *testing.T, once bool) (*Sender, *refusalRecord) {
	t.Helper()

	rec := &refusalRecord{}
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })

	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		if once {
			_ = lis.Close()
		}
		defer conn.Close()

		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(conn, preface); err != nil {
			return
		}
		fr := http2.NewFramer(conn, conn)
		if err := fr.WriteSettings(); err != nil {
			return
		}
		sawData := make(map[uint32]bool)
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			// Recorded apart from the refusal, so a refusal moved before the
			// DATA shows in the record.
			if d, ok := f.(*http2.DataFrame); ok {
				sawData[d.StreamID] = true
			}
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					_ = fr.WriteSettingsAck()
				}
			case *http2.DataFrame:
				// GOAWAY first: the client stops opening streams on this
				// connection before it sees the refusal, so the retry cannot
				// be written here before the hang-up (5 in 300 on go1.25.0,
				// -race, 2 CPUs).
				if !rec.refused.Swap(true) {
					rec.dataBeforeRefusal.Store(sawData[f.StreamID])
				}
				if once {
					_ = fr.WriteGoAway(f.StreamID, http2.ErrCodeNo, nil)
				}
				_ = fr.WriteRSTStream(f.StreamID, http2.ErrCodeRefusedStream)
				if once {
					return
				}
			}
		}
	}()

	sender := New(Options{Target: "passthrough:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithStatsHandler(rec),
	}})
	t.Cleanup(func() { _ = sender.Close() })
	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	return sender, rec
}

// refusalRecord notes whether the stand got the first attempt's DATA and
// whether grpc-go reported its OutPayload before that attempt ended.
type refusalRecord struct {
	refused           atomic.Bool
	dataBeforeRefusal atomic.Bool
	payloadBeforeEnd  atomic.Bool
	firstEnded        atomic.Bool
}

func (r *refusalRecord) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (r *refusalRecord) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (r *refusalRecord) HandleConn(context.Context, stats.ConnStats) {}

func (r *refusalRecord) HandleRPC(_ context.Context, s stats.RPCStats) {
	switch s.(type) {
	case *stats.OutPayload:
		if !r.firstEnded.Load() {
			r.payloadBeforeEnd.Store(true)
		}
	case *stats.End:
		r.firstEnded.Store(true)
	}
}

// As the state in #75, the last attempt decides whether the call went out: the
// first went out and was refused unprocessed, the retry never got written.
// A sticky "went out" would say the target may have processed a call that
// REFUSED_STREAM guarantees it did not.
func TestSend_WentOutIsTheLastAttempts(t *testing.T) {
	sender, rec := refusingAfterPayload(t, true)
	out := sendWithin(t, sender, time.Second)

	// The scenario: the first attempt went out — its DATA reached the stand and
	// grpc-go reported its payload — and only then was it refused.
	if !rec.dataBeforeRefusal.Load() || !rec.payloadBeforeEnd.Load() {
		t.Fatalf("DATA at the stand %v, OutPayload before the first End %v: the first attempt did not go out",
			rec.dataBeforeRefusal.Load(), rec.payloadBeforeEnd.Load())
	}
	if out.Category != engine.CategoryUnreachable {
		t.Errorf("category = %v (code %s), want unreachable: the retry went nowhere", out.Category, out.Code)
	}
}

// REFUSED_STREAM on the last attempt means unprocessed, yet the call is cut off
// with "may have processed": the safe side, a user checks for duplicates rather
// than assumes none. Telling it apart needs the status text; that is tech debt.
func TestSend_RefusedOnTheLastAttemptIsCutOff(t *testing.T) {
	sender, _ := refusingAfterPayload(t, false)
	out := sendWithin(t, sender, time.Second)

	if out.Category != engine.CategoryCutOff {
		t.Errorf("category = %v (code %s, %v), want cut off", out.Category, out.Code, out.Err)
	}
}

// The cell #85 lacked: a trailer came over the wire, it said OK, and the code
// in the error is one our client set on the reply it refused. The category is
// the client's, and so is the code's source.
func TestCategorize_ATrailerSayingOKWithAReplyTheClientRefused(t *testing.T) {
	for _, err := range []error{
		status.Error(codes.Internal, `grpc: Decompressor is not installed for grpc-encoding "gzip"`),
		status.Error(codes.Internal, "grpc: failed to decompress the received message: gzip: invalid header"),
	} {
		if got := categorize(err, true, true); got != engine.CategoryBadResponse {
			t.Errorf("%v with a trailer: category %v, want a bad response", err, got)
		}
		if !refusedReply(err, true) {
			t.Errorf("%v with a trailer: not seen as the client's, so the code would read as the target's", err)
		}
	}
}
