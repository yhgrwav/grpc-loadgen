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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	method   = "grpc.health.v1.Health/Check"
	rps      = 200
	duration = 30 * time.Second
	timeout  = 5 * time.Second
)

// mode is a stand behavior and the tools run against it.
type mode struct {
	name     string
	stand    []string
	variants []string
}

var modes = []mode{
	{"A", []string{"-delay", "10ms"}, []string{"leettest", "ghz-sync", "ghz-async"}},
	{"B1", []string{"-delay", "10ms", "-freeze-at", "10s", "-freeze-for", "2s"}, []string{"leettest", "ghz-sync", "ghz-async"}},
	{"B2", []string{"-delay", "100ms"}, []string{"leettest", "ghz-sync"}},
}

// result is one run's numbers; latencies are zero where the tool gave none.
type result struct {
	sent, failed       int
	p50, p90, p95, p99 time.Duration
	// over1s counts calls slower than a second, from ghz's per-call details;
	// -1 for LeetTest, which prints no per-call data.
	over1s int
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "compare-ghz:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	runs := flag.Int("runs", 5, "runs of each tool in each mode")
	out := flag.String("out", filepath.Join("test", "compare-ghz", "out"), "directory for binaries and raw outputs")
	ghz := flag.String("ghz", "ghz", "path to ghz")
	only := flag.String("modes", "A,B1,B2", "modes to run, comma-separated")
	flag.Parse()

	bin := filepath.Join(*out, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		return err
	}
	stand, leettest := filepath.Join(bin, "stand"+exe()), filepath.Join(bin, "leettest"+exe())
	for target, pkg := range map[string]string{stand: "./test/stand/cmd/stand", leettest: "./cmd/leettest"} {
		if b, err := exec.CommandContext(ctx, "go", "build", "-o", target, pkg).CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %w: %s", pkg, err, b)
		}
	}

	var summary strings.Builder
	for _, m := range modes {
		if !slices.Contains(strings.Split(*only, ","), m.name) {
			continue
		}
		results := map[string][]result{}
		for i := range *runs {
			// Rotate the order so no tool always runs first or last.
			for j := range m.variants {
				v := m.variants[(i+j)%len(m.variants)]
				raw := filepath.Join(*out, fmt.Sprintf("%s-%s-%d", m.name, v, i+1))
				r, err := once(ctx, stand, leettest, *ghz, m, v, raw)
				if err != nil {
					return fmt.Errorf("%s %s run %d: %w", m.name, v, i+1, err)
				}
				fmt.Printf("%s %-9s run %d: %s\n", m.name, v, i+1, r)
				results[v] = append(results[v], r)
			}
		}
		table(&summary, m, results)
	}

	fmt.Print("\n", summary.String())

	return os.WriteFile(filepath.Join(*out, "summary.md"), []byte(summary.String()), 0o600)
}

func exe() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}

	return ""
}

// once starts a fresh stand, so its freeze counts from this run's first call,
// runs one tool against it and stops the stand.
func once(ctx context.Context, stand, leettest, ghz string, m mode, variant, raw string) (result, error) {
	addr, err := freeAddr(ctx)
	if err != nil {
		return result{}, err
	}

	srv := exec.CommandContext(ctx, stand, append([]string{"-addr", addr}, m.stand...)...)
	if err := srv.Start(); err != nil {
		return result{}, err
	}
	defer func() { _ = srv.Process.Kill(); _ = srv.Wait() }()
	if err := waitListening(ctx, addr); err != nil {
		return result{}, err
	}

	if variant == "leettest" {
		return runLeetTest(ctx, leettest, addr, raw)
	}

	return runGhz(ctx, ghz, addr, variant == "ghz-async", raw)
}

func freeAddr(ctx context.Context) (string, error) {
	lis, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer lis.Close()

	return lis.Addr().String(), nil
}

func waitListening(ctx context.Context, addr string) error {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", addr); err == nil {
			return conn.Close()
		}
		time.Sleep(50 * time.Millisecond) // polling a port nothing signals
	}

	return fmt.Errorf("stand is not listening on %s", addr)
}

func runLeetTest(ctx context.Context, bin, addr, raw string) (result, error) {
	host, port, _ := net.SplitHostPort(addr)
	cfg := fmt.Sprintf("app:\n  target:\n    ip: %s\n    port: %s\n  tls: false\n\nload:\n  calls:\n"+
		"    - method: %s\n      rps: %d\n      duration: %s\n      timeout: %s\n",
		host, port, method, rps, duration, timeout)
	cfgPath := raw + ".yaml"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return result{}, err
	}

	cmd := exec.CommandContext(ctx, bin, "-c", cfgPath)
	stdout, runErr := cmd.Output()
	if err := os.WriteFile(raw+".txt", stdout, 0o600); err != nil {
		return result{}, err
	}
	var exit *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exit) {
		return result{}, runErr
	}

	return parseLeetTest(string(stdout))
}

// parseLeetTest reads the totals line and the method's row of the text report.
func parseLeetTest(text string) (result, error) {
	r := result{over1s: -1}
	found := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "sent ") {
			if _, err := fmt.Sscanf(line, "sent %d, failed %d", &r.sent, &r.failed); err != nil {
				return r, fmt.Errorf("totals %q: %w", line, err)
			}
		}
		f := strings.Fields(line)
		if len(f) == 8 && f[0] == method {
			var err error
			ps := []*time.Duration{&r.p50, &r.p90, &r.p95, &r.p99}
			for i, p := range ps {
				if *p, err = parseLatency(f[4+i]); err != nil {
					return r, err
				}
			}
			found = true
		}
	}
	if !found {
		return r, fmt.Errorf("no row for %s in:\n%s", method, text)
	}

	return r, nil
}

// parseLatency reads a latency as the report prints it; a bound (">1.0s") is
// read as its value.
func parseLatency(s string) (time.Duration, error) {
	if s == "-" {
		return 0, nil
	}

	return time.ParseDuration(strings.TrimPrefix(s, ">"))
}

type ghzReport struct {
	Count               int            `json:"count"`
	ErrorDistribution   map[string]int `json:"errorDistribution"`
	LatencyDistribution []struct {
		Percentage int           `json:"percentage"`
		Latency    time.Duration `json:"latency"`
	} `json:"latencyDistribution"`
	Details []struct {
		Latency time.Duration `json:"latency"`
	} `json:"details"`
}

func runGhz(ctx context.Context, bin, addr string, async bool, raw string) (result, error) {
	args := []string{
		"--insecure", "--call", method, "-d", "{}",
		"--rps", strconv.Itoa(rps), "-z", duration.String(), "-t", timeout.String(),
		"--duration-stop", "wait", "--connections", "1", "-c", "10",
		"-O", "json", "-o", raw + ".json",
	}
	if async {
		args = append(args, "--async")
	}
	args = append(args, addr)

	ctx, cancel := context.WithTimeout(ctx, duration+time.Minute)
	defer cancel()
	if b, err := exec.CommandContext(ctx, bin, args...).CombinedOutput(); err != nil {
		return result{}, fmt.Errorf("ghz: %w: %s", err, b)
	}

	data, err := os.ReadFile(raw + ".json")
	if err != nil {
		return result{}, err
	}
	var rep ghzReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return result{}, err
	}

	r := result{sent: rep.Count}
	for _, n := range rep.ErrorDistribution {
		r.failed += n
	}
	for _, l := range rep.LatencyDistribution {
		switch l.Percentage {
		case 50:
			r.p50 = l.Latency
		case 90:
			r.p90 = l.Latency
		case 95:
			r.p95 = l.Latency
		case 99:
			r.p99 = l.Latency
		}
	}
	for _, d := range rep.Details {
		if d.Latency > time.Second {
			r.over1s++
		}
	}

	return r, nil
}

func (r result) String() string {
	return fmt.Sprintf("sent %d, failed %d, p50 %v, p90 %v, p95 %v, p99 %v, over 1s %d",
		r.sent, r.failed, r.p50, r.p90, r.p95, r.p99, r.over1s)
}

// table writes the median and the range of each number over the runs.
func table(w *strings.Builder, m mode, results map[string][]result) {
	fmt.Fprintf(w, "### %s (stand %s)\n\n", m.name, strings.Join(m.stand, " "))
	fmt.Fprintln(w, "| variant | sent | failed | p50 | p90 | p95 | p99 | over 1s |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|")
	for _, v := range m.variants {
		rs := results[v]
		ints := func(get func(result) int) string {
			xs := make([]int, len(rs))
			for i, r := range rs {
				xs[i] = get(r)
			}
			slices.Sort(xs)

			return fmt.Sprintf("%d [%d–%d]", xs[len(xs)/2], xs[0], xs[len(xs)-1])
		}
		durs := func(get func(result) time.Duration) string {
			xs := make([]time.Duration, len(rs))
			for i, r := range rs {
				xs[i] = get(r)
			}
			slices.Sort(xs)
			round := func(d time.Duration) time.Duration { return d.Round(100 * time.Microsecond) }

			return fmt.Sprintf("%v [%v–%v]", round(xs[len(xs)/2]), round(xs[0]), round(xs[len(xs)-1]))
		}
		// LeetTest prints no per-call data: a count of -1 would read as a number.
		over1s := "n/a"
		if rs[0].over1s >= 0 {
			over1s = ints(func(r result) int { return r.over1s })
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", v,
			ints(func(r result) int { return r.sent }), ints(func(r result) int { return r.failed }),
			durs(func(r result) time.Duration { return r.p50 }), durs(func(r result) time.Duration { return r.p90 }),
			durs(func(r result) time.Duration { return r.p95 }), durs(func(r result) time.Duration { return r.p99 }),
			over1s)
	}
	fmt.Fprintln(w)
}
