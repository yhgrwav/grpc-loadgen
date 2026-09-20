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

package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

// Interactive reports whether the live view can be drawn.
func Interactive() bool {
	return isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())
}

// RunPlain executes the run without the live view, printing progress lines
// instead. It is used when stderr is not a terminal.
func RunPlain(w io.Writer, target string, eng *engine.Engine, run func() error) error {
	fmt.Fprintf(w, "running against %s\n", target)

	done := make(chan error, 1)
	go func() {
		done <- run()
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			s := eng.Snapshot()
			fmt.Fprintf(w, "%s  sent %d  rps %.0f  in-flight %d  failed %d  p99 %s\n",
				formatDuration(s.Elapsed), s.Sent, s.RPS, s.InFlight, s.Failed, formatQuantile(s.P99))
		}
	}
}
