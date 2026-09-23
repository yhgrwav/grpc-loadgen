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
	"encoding/binary"
	"math"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/yhgrwav/leettest/pkg/engine"
)

const (
	frameHeaderLen = 9
	// maxSettingsLen bounds what the reader buffers: RFC 9113 §4.2 lets no
	// frame exceed 16384 bytes before the peer raises the limit, and nobody
	// has raised it before the first frame.
	maxSettingsLen = 16384

	typeSettings       = 0x4
	settingMaxStreams  = 0x3
	settingEntryLength = 6
)

// settingsReader picks the stream limit out of the target's first HTTP/2
// frame, which RFC 9113 §3.4 requires to be SETTINGS.
type settingsReader struct {
	buf []byte
	// limit and announced are the result, valid once feed reports done.
	limit     uint32
	announced bool
}

// feed takes the next bytes the target sent, however the reads cut them, and
// reports whether the first frame is complete. Bytes past it are ignored.
func (r *settingsReader) feed(p []byte) (done bool) {
	r.buf = append(r.buf, p...)
	if len(r.buf) < frameHeaderLen {
		return false
	}

	length := int(r.buf[0])<<16 | int(r.buf[1])<<8 | int(r.buf[2])
	if r.buf[3] != typeSettings || length > maxSettingsLen {
		r.buf = nil

		return true
	}
	if len(r.buf) < frameHeaderLen+length {
		return false
	}

	payload := r.buf[frameHeaderLen : frameHeaderLen+length]
	for i := 0; i+settingEntryLength <= len(payload); i += settingEntryLength {
		if binary.BigEndian.Uint16(payload[i:]) == settingMaxStreams {
			r.limit, r.announced = binary.BigEndian.Uint32(payload[i+2:]), true
		}
	}
	r.buf = nil

	return true
}

// handshake is what one connection's first SETTINGS said.
type handshake struct {
	limit     uint32
	announced bool
}

// connTracker counts the handshakes the sender went through and what each
// announced.
type connTracker struct {
	mu         sync.Mutex
	handshakes int
	// heard counts handshakes whose first SETTINGS has been read.
	heard   int
	first   handshake
	last    handshake
	changes int
}

func (c *connTracker) connected() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.handshakes++
}

func (c *connTracker) announced(h handshake) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heard == 0 {
		c.first = h
	} else if h != c.last {
		c.changes++
	}
	c.last = h
	c.heard++
}

func (c *connTracker) report() (engine.Connections, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heard == 0 {
		return engine.Connections{}, false
	}

	// grpc-go's default pick_first balancer keeps one connection per target.
	return engine.Connections{
		Open:           1,
		Reconnects:     c.handshakes - 1,
		LimitAnnounced: c.last.announced,
		FirstLimit:     c.first.limit,
		LastLimit:      c.last.limit,
		LimitChanges:   c.changes,
	}, true
}

// settingsConn reads the target's first frame as it passes. After it, a
// read costs one bool check. Only the transport's reader goroutine reads.
type settingsConn struct {
	net.Conn

	reader  settingsReader
	done    bool
	tracker *connTracker
}

func (c *settingsConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if !c.done && n > 0 && c.reader.feed(p[:n]) {
		c.done = true
		c.tracker.announced(handshake{limit: c.reader.limit, announced: c.reader.announced})
	}

	return n, err
}

// trackingCreds wraps the transport credentials so every connection passes
// through settingsConn once it is secured: under TLS the frames are only
// readable after the handshake.
type trackingCreds struct {
	credentials.TransportCredentials

	tracker *connTracker
}

func (c trackingCreds) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (
	net.Conn, credentials.AuthInfo, error,
) {
	conn, info, err := c.TransportCredentials.ClientHandshake(ctx, authority, raw)
	if err != nil {
		return conn, info, err
	}

	c.tracker.connected()

	return &settingsConn{Conn: conn, tracker: c.tracker}, info, nil
}

func (c trackingCreds) Clone() credentials.TransportCredentials {
	return trackingCreds{TransportCredentials: c.TransportCredentials.Clone(), tracker: c.tracker}
}

// readyWindow remembers when the connection last became ready and last
// stopped being ready.
type readyWindow struct {
	mu      sync.Mutex
	enterAt time.Time
	leftAt  time.Time
}

func (w *readyWindow) entered(at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.enterAt = at
}

func (w *readyWindow) left(at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.leftAt = at
}

// throughout reports whether the connection was ready for the whole of a call
// begun at begun, as seen now: the last entry into READY no later than begun
// and no exit since. Anything else, a watcher running late included, is not
// proof.
func (w *readyWindow) throughout(begun time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return !w.enterAt.IsZero() && !w.enterAt.After(begun) && w.leftAt.Before(w.enterAt)
}

// Connections reports the connections the run went over. False when no
// handshake passed through the sender's own credentials, as with credentials
// of the caller's in DialOptions. See engine.ConnectionReporter.
func (s *Sender) Connections() (engine.Connections, bool) {
	return s.tracker.report()
}

// limit is the stream limit of the last handshake heard; MaxUint32, the
// client's own quota, when none was announced or none was heard.
func (c *connTracker) limit() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heard == 0 || !c.last.announced {
		return math.MaxUint32
	}

	return c.last.limit
}
