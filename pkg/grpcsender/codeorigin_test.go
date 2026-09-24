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
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// rawTarget serves raw HTTP/2 and hands each request's HEADERS to onHeaders,
// numbered from 1: the test decides what the other end does with the stream.
// hangUp, when set, closes the connection right after onHeaders.
func rawTarget(t *testing.T, onHeaders func(fr *http2.Framer, stream uint32, n int), hangUp ...bool) *Sender {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go serveRaw(conn, onHeaders, len(hangUp) > 0 && hangUp[0])
		}
	}()

	sender := New(Options{Target: "passthrough:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
	}})
	t.Cleanup(func() { _ = sender.Close() })
	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	return sender
}

func serveRaw(conn net.Conn, onHeaders func(fr *http2.Framer, stream uint32, n int), hangUp bool) {
	defer conn.Close()

	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return
	}
	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		return
	}

	n := 0
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				_ = fr.WriteSettingsAck()
			}
		case *http2.PingFrame:
			if !f.IsAck() {
				_ = fr.WritePing(true, f.Data)
			}
		case *http2.HeadersFrame:
			n++
			onHeaders(fr, f.StreamID, n)
			if hangUp {
				return
			}
		}
	}
}

// trailersOnly answers a stream with a status and nothing else, as nginx's
// error_page stub does.
func trailersOnly(fr *http2.Framer, stream uint32, code codes.Code) {
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, h := range [][2]string{{":status", "200"}, {"content-type", "application/grpc"}, {"grpc-status", strconv.Itoa(int(code))}} {
		_ = enc.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]})
	}
	_ = fr.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndStream: true, EndHeaders: true})
}

// sendWithin sends one call with the given deadline from now; 0 means none.
func sendWithin(t *testing.T, s *Sender, deadline time.Duration) engine.Outcome {
	t.Helper()

	req := request(time.Now())
	if deadline != 0 {
		req.Deadline = time.Now().Add(deadline)
	}
	out, err := s.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	return out
}

// A code is the target's only when a status came back over the wire. grpc-go
// sets the same codes itself: Unavailable when nothing answered, Internal for a
// reset stream, DeadlineExceeded at our own deadline. Printed together, a code
// set here reads as the target's.
//
// Ground: contract — engine.Outcome.CodeFromTarget is public.
func TestSend_TellsACodeThatCameBackFromOneTheClientSet(t *testing.T) {
	cases := []struct {
		name       string
		send       func(t *testing.T) engine.Outcome
		code       codes.Code
		fromTarget bool
	}{
		{"target sent Unavailable", func(t *testing.T) engine.Outcome {
			return sendWithin(t, dialTarget(t, &target{code: codes.Unavailable}), 0)
		}, codes.Unavailable, true},
		// No deadline of ours: the target's DeadlineExceeded cannot race our timer.
		{"target sent DeadlineExceeded", func(t *testing.T) engine.Outcome {
			return sendWithin(t, dialTarget(t, &target{code: codes.DeadlineExceeded}), 0)
		}, codes.DeadlineExceeded, true},
		{"a proxy's trailers-only status", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(fr *http2.Framer, id uint32, _ int) { trailersOnly(fr, id, codes.Unavailable) }), 0)
		}, codes.Unavailable, true},
		{"nothing answered", func(t *testing.T) engine.Outcome {
			return sendWithin(t, vanishedTarget(t), 0)
		}, codes.Unavailable, false},
		// The other end reset the stream: no status came back, grpc-go set the code.
		{"stream reset with INTERNAL_ERROR", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(fr *http2.Framer, id uint32, _ int) { _ = fr.WriteRSTStream(id, http2.ErrCodeInternal) }), 0)
		}, codes.Internal, false},
		// A target that never answers: only our timer can end the call.
		{"our deadline ran out", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(*http2.Framer, uint32, int) {}), 50*time.Millisecond)
		}, codes.DeadlineExceeded, false},
		{"deadline past before sending", func(t *testing.T) engine.Outcome {
			return sendWithin(t, dialTarget(t, &target{}), -time.Millisecond)
		}, codes.DeadlineExceeded, false},
		// As the state in #75, the last attempt decides: the first was refused
		// unprocessed and retried transparently, the retry got a status.
		{"refused, retried, answered", func(t *testing.T) engine.Outcome {
			return sendWithin(t, rawTarget(t, func(fr *http2.Framer, id uint32, n int) {
				if n == 1 {
					_ = fr.WriteRSTStream(id, http2.ErrCodeRefusedStream)

					return
				}
				trailersOnly(fr, id, codes.NotFound)
			}), 2*time.Second)
		}, codes.NotFound, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := c.send(t)
			if out.Code != c.code.String() {
				t.Fatalf("code = %q, want %q: the case no longer pins its code", out.Code, c.code)
			}
			if out.CodeFromTarget != c.fromTarget {
				t.Errorf("CodeFromTarget = %v, want %v", out.CodeFromTarget, c.fromTarget)
			}
		})
	}
}
