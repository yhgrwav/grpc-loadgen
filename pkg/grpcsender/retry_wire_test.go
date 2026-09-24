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
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/test/bufconn"
)

// refusingOnce is a raw HTTP/2 target: it refuses the first stream with
// RST_STREAM REFUSED_STREAM and answers every later one trailers-only with
// NOT_FOUND. RFC 9113 §8.7: a refused stream was not processed.
func refusingOnce(t *testing.T, conn net.Conn) {
	t.Helper()

	defer conn.Close()

	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return
	}

	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		return
	}

	refused := false
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
			if !refused {
				refused = true
				_ = fr.WriteRSTStream(f.StreamID, http2.ErrCodeRefusedStream)

				continue
			}

			var block bytes.Buffer
			enc := hpack.NewEncoder(&block)
			for _, h := range [][2]string{{":status", "200"}, {"content-type", "application/grpc"}, {"grpc-status", "5"}} {
				_ = enc.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]})
			}
			_ = fr.WriteHeaders(http2.HeadersFrameParam{
				StreamID: f.StreamID, BlockFragment: block.Bytes(), EndStream: true, EndHeaders: true,
			})
		}
	}
}

// eventLog keeps the attempt events grpc-go reports for calls.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (l *eventLog) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (l *eventLog) HandleConn(context.Context, stats.ConnStats) {}

func (l *eventLog) HandleRPC(_ context.Context, s stats.RPCStats) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch v := s.(type) {
	case *stats.Begin:
		if v.IsTransparentRetryAttempt {
			l.events = append(l.events, "Begin(retry)")
		} else {
			l.events = append(l.events, "Begin")
		}
	case *stats.OutHeader:
		l.events = append(l.events, "OutHeader")
	case *stats.End:
		l.events = append(l.events, "End")
	}
}

// Ground: signal grpc-go v1.84.0 — the TestRetry_* tests feed the handler a sequence of stats
// events; this pins that grpc-go really sends it. A stream refused with REFUSED_STREAM is marked
// unprocessed (internal/transport/http2_client.go:1302) and retried transparently on the same
// connection (stream.go:816), with Begin and End for each attempt. Were End sent once per call,
// every retry would leave the gauge at +1 for good.
func TestRetry_GrpcGoReportsEachTransparentAttempt(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go refusingOnce(t, conn)
		}
	}()
	t.Cleanup(func() { _ = lis.Close() })

	log := &eventLog{}
	sender := New(Options{Target: "passthrough:///bufnet", DialOptions: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithStatsHandler(log),
	}})
	t.Cleanup(func() { _ = sender.Close() })

	if err := sender.Connect(bounded(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	req := request(time.Now())
	req.Deadline = req.ScheduledAt.Add(2 * time.Second)

	out, err := sender.Send(bounded(t), req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	log.mu.Lock()
	events := append([]string(nil), log.events...)
	log.mu.Unlock()

	want := []string{"Begin", "OutHeader", "End", "Begin(retry)", "OutHeader", "End"}
	if len(events) != len(want) {
		t.Fatalf("events %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events %v, want %v", events, want)
		}
	}

	if out.NotSent {
		t.Errorf("not sent = true: the retry went out and the target answered (%v)", out.Err)
	}
	if out.Code != "NotFound" {
		t.Errorf("code %s, want the retry's NotFound", out.Code)
	}
	if n := sender.OpenStreams(); n != 0 {
		t.Errorf("open streams %d after the call, want 0", n)
	}
}
