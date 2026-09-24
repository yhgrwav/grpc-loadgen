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

package main

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

// lineWith reports whether some line of text holds every part.
func lineWith(text string, parts ...string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(line, p)
		}
		if all {
			return true
		}
	}

	return false
}

// passCodec moves bytes as they are, so the server below needs no schema.
type passCodec struct{}

func (passCodec) Marshal(v any) ([]byte, error)      { return *(v.(*[]byte)), nil }
func (passCodec) Unmarshal(data []byte, v any) error { *(v.(*[]byte)) = data; return nil }
func (passCodec) Name() string                       { return "proto" }

var _ encoding.Codec = passCodec{}

const bigMethod = "leettest.test.Big/Get"

// startBigTarget answers any method with size bytes.
func startBigTarget(t *testing.T, size int) string {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := grpc.NewServer(grpc.ForceServerCodec(passCodec{}), grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		var in []byte
		if err := stream.RecvMsg(&in); err != nil {
			return err
		}

		out := make([]byte, size)

		return stream.SendMsg(&out)
	}))

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return lis.Addr().String()
}

// A reply over the transport's 4 MiB fails every call; app.max_response_size
// raised above it lets every one through.
func TestRun_MaxResponseSizeLetsALargeReplyThrough(t *testing.T) {
	addr := startBigTarget(t, 5<<20)

	for _, c := range []struct {
		name, line string
		failAll    bool
	}{
		{"default limit", plaintext, true},
		{"raised limit", plaintext + "\n  max_response_size: 8MiB", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := runCLI(t.Context(), t, 10*time.Second, "-c", writeConfig(t, addr, bigMethod, c.line))
			// Every call of the only method rejected is an invalid run.
			if errors.Is(res.err, ErrInvalidRun) != c.failAll || (res.err != nil && !c.failAll) {
				t.Fatalf("run: %v, want invalid = %v\nstderr:\n%s", res.err, c.failAll, res.stderr)
			}

			sent, failed := reportRow(t, res.stdout, bigMethod)
			if sent == 0 {
				t.Fatal("nothing sent")
			}
			if (failed == sent) != c.failAll || (failed == 0) == c.failAll {
				t.Errorf("failed %d of %d, want all failed = %v", failed, sent, c.failAll)
			}
			if !c.failAll {
				return
			}
			// The client refused the reply after it came in whole: not cut off
			// (#85 leaves size out), and the code is the client's, not the target's.
			if strings.Contains(res.stdout, "cut off") || strings.Contains(res.stdout, "codes sent by the target") {
				t.Errorf("an oversized reply reads as cut off or as the target's code:\n%s", res.stdout)
			}
			if !lineWith(res.stdout, "codes set by the client", "ResourceExhausted") {
				t.Errorf("ResourceExhausted is not among the codes the client set:\n%s", res.stdout)
			}
		})
	}
}
