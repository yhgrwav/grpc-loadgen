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
	"time"

	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

// PrintReport writes the finished run to w as plain text.
func PrintReport(w io.Writer, target string, report engine.Report) {
	fmt.Fprintf(w, "run finished: %s in %s\n", target, formatDuration(report.Duration))
	fmt.Fprintf(w, "sent %d, failed %d\n\n", report.Sent, report.Failed)

	fmt.Fprintf(w, "%-44s %8s %8s %9s %9s %9s %9s\n",
		"method", "sent", "failed", "rps", "p50", "p95", "p99")

	for _, m := range report.Methods {
		fmt.Fprintf(w, "%-44s %8d %8d %9.0f %9s %9s %9s\n",
			m.Method, m.Sent, m.Failed, m.RPS,
			formatDuration(m.P50), formatDuration(m.P95), formatDuration(m.P99))
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fus", float64(d.Nanoseconds())/1e3)
	case d < time.Second:
		return fmt.Sprintf("%.0fms", float64(d.Nanoseconds())/1e6)
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}
