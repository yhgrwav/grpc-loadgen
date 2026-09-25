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
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/yhgrwav/leettest/pkg/engine"
)

func replyHeaders(fr *http2.Framer, stream uint32, extra ...[2]string) {
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, h := range append([][2]string{{":status", "200"}, {"content-type", "application/grpc"}}, extra...) {
		_ = enc.WriteField(hpack.HeaderField{Name: h[0], Value: h[1]})
	}
	_ = fr.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndHeaders: true})
}

func okTrailers(fr *http2.Framer, stream uint32) {
	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	_ = enc.WriteField(hpack.HeaderField{Name: "grpc-status", Value: "0"})
	_ = fr.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndStream: true, EndHeaders: true})
}

// A reply's headers, then a reset: no reply came, whatever the headers said.
// Ground: stand with known behavior — the reviewer's counterexample to
// "headers arrived" as the test for a bad response.
func TestSend_HeadersThenAResetIsCutOffNotABadResponse(t *testing.T) {
	out := sendWithin(t, rawSender(t, func(conn net.Conn) {
		serveRawData(conn, func(*http2.Framer, uint32, int) {}, func(fr *http2.Framer, id uint32) bool {
			replyHeaders(fr, id)
			_ = fr.WriteRSTStream(id, http2.ErrCodeInternal)

			return false
		})
	}), 2*time.Second)

	if out.Category != engine.CategoryCutOff {
		t.Errorf("category %v (%v), want cut off", out.Category, out.Err)
	}
}

// A body compressed with gzip, which our client does not register: the reply
// came, the target said OK, and the client could not read it.
// Ground: stand with known behavior.
func TestSend_ABodyTheClientCannotDecompressIsABadResponse(t *testing.T) {
	out := sendWithin(t, rawSender(t, func(conn net.Conn) {
		serveRawData(conn, func(*http2.Framer, uint32, int) {}, func(fr *http2.Framer, id uint32) bool {
			replyHeaders(fr, id, [2]string{"grpc-encoding", "gzip"})
			junk := []byte("not gzip at all")
			frame := make([]byte, 5, 5+len(junk))
			frame[0] = 1 // compressed
			binary.BigEndian.PutUint32(frame[1:], uint32(len(junk)))
			_ = fr.WriteData(id, false, append(frame, junk...))
			okTrailers(fr, id)

			return false
		})
	}), 2*time.Second)

	t.Logf("err: %v", out.Err)
	if out.Category != engine.CategoryBadResponse {
		t.Errorf("category %v (%v), want a bad response", out.Category, out.Err)
	}
	if out.CodeFromTarget {
		t.Errorf("CodeFromTarget = true: the target said OK, the client set %s", out.Code)
	}
	if out.SentAt.IsZero() || out.DoneAt.IsZero() || out.DoneAt.Before(out.SentAt) {
		t.Errorf("SentAt %v, DoneAt %v: a bad response has a real latency", out.SentAt, out.DoneAt)
	}
}
