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
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/yhgrwav/leettest/pkg/engine"
)

// closedWindow is a raw HTTP/2 target that announces a stream window of 0
// and never opens it: the request's HEADERS go out, its DATA never can.
// headers is closed when the first HEADERS arrive.
func closedWindow(conn net.Conn, headers chan<- struct{}) {
	defer conn.Close()

	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return
	}
	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 0}); err != nil {
		return
	}

	seen := false
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
			if !seen {
				seen = true
				close(headers)
			}
		}
	}
}

// A target that never opens its flow window holds the request: it saw the
// call and does not let it finish. The call times out as one that went out —
// the target's time, not a wait in the generator or on the connection — and
// the wait is not a stream wait: a stream was granted.
//
// Ground: signal grpc-go v1.84.0 — with SETTINGS_INITIAL_WINDOW_SIZE 0 (RFC 9113 §6.9.2) the
// stream opens and OutHeader fires, while the DATA waits in the transport; a raw target is the
// only way to hold a window shut.
func TestSend_AClosedFlowWindowIsTheTargetsTimeout(t *testing.T) {
	headers := make(chan struct{})
	s := rawSender(t, func(conn net.Conn) { closedWindow(conn, headers) })

	out := sendWithin(t, s, 300*time.Millisecond)
	select {
	case <-headers:
	default:
		t.Fatal("no HEADERS reached the target: the case no longer tests a closed window")
	}

	if out.Category != engine.CategoryTimeout {
		t.Errorf("category = %v, want timeout (%v)", out.Category, out.Err)
	}
	if out.NotSent || out.SentAt.IsZero() {
		t.Errorf("not sent = %v, sent at %v: the HEADERS went out, the target held the rest", out.NotSent, out.SentAt)
	}
	if out.CodeFromTarget {
		t.Errorf("code %s marked as the target's: no status came back", out.Code)
	}
	if out.StreamWait > 50*time.Millisecond {
		t.Errorf("stream wait %v: the stream was granted, the window was not", out.StreamWait)
	}
}
