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

// Command stand serves the reference stand over TCP, so the CLI or another
// load tool can be pointed at a target whose behavior is known in advance.
// On exit it prints what it saw: arrivals per second and how long it held
// the calls it answered.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/yhgrwav/leettest/test/stand"
)

type options struct {
	addr      string
	delay     time.Duration
	freezeAt  time.Duration
	freezeFor time.Duration
	hangFrom  time.Duration
	failEvery int
	life      time.Duration
}

func parse(args []string, usage io.Writer) (options, error) {
	var o options

	fs := flag.NewFlagSet("stand", flag.ContinueOnError)
	fs.SetOutput(usage)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:50051", "address to listen on")
	fs.DurationVar(&o.delay, "delay", 0, "hold every answer this long")
	fs.DurationVar(&o.freezeAt, "freeze-at", 0, "start of a freeze, after the first arrival; with -freeze-for")
	fs.DurationVar(&o.freezeFor, "freeze-for", 0, "freeze length: calls arriving in it wait until it ends")
	fs.DurationVar(&o.hangFrom, "hang-from", -1, "never answer from this long after the first arrival")
	fs.IntVar(&o.failEvery, "fail-every", 0, "answer every n-th call with RESOURCE_EXHAUSTED")
	fs.DurationVar(&o.life, "life", 0, "exit after this long; 0 waits for Ctrl+C")

	if err := fs.Parse(args); err != nil {
		return o, err
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var modes []string
	for _, name := range []string{"freeze-for", "hang-from", "fail-every"} {
		if set[name] {
			modes = append(modes, "-"+name)
		}
	}

	switch {
	case len(modes) > 1:
		return o, fmt.Errorf("one behavior at a time, got %s", strings.Join(modes, " and "))
	case set["freeze-at"] && !set["freeze-for"]:
		return o, errors.New("-freeze-at needs -freeze-for")
	case o.delay < 0, o.freezeAt < 0, o.freezeFor < 0, o.life < 0, o.failEvery < 0,
		set["hang-from"] && o.hangFrom < 0:
		return o, errors.New("durations and -fail-every must not be negative")
	}

	return o, nil
}

func (o options) answer() stand.Answer {
	switch {
	case o.freezeFor > 0:
		return stand.Frozen(o.freezeAt, o.freezeFor, o.delay)
	case o.hangFrom >= 0:
		return stand.HangingFrom(o.hangFrom, o.delay)
	case o.failEvery > 0:
		return stand.FailEvery(o.failEvery, codes.ResourceExhausted, o.delay)
	default:
		return stand.Constant(o.delay)
	}
}

// run serves until stop closes or the lifetime runs out, then prints the
// summary to out. ready gets the address actually listened on.
func run(args []string, out, errOut io.Writer, stop <-chan struct{}, ready func(string)) error {
	o, err := parse(args, errOut)
	if err != nil {
		return err
	}

	lis, err := new(net.ListenConfig).Listen(context.Background(), "tcp", o.addr)
	if err != nil {
		return err
	}

	s := stand.StartOn(lis, o.answer())
	fmt.Fprintf(errOut, "stand on %s, method grpc.health.v1.Health/Check\n", s.Target())
	ready(s.Target())

	var expired <-chan time.Time
	if o.life > 0 {
		expired = time.After(o.life)
	}
	select {
	case <-stop:
	case <-expired:
	}
	s.Stop()

	summarize(out, s.Arrivals(), s.Holds())

	return nil
}

func summarize(w io.Writer, arrivals []time.Time, holds []time.Duration) {
	fmt.Fprintf(w, "arrivals %d, answered %d\n", len(arrivals), len(holds))

	if len(arrivals) > 0 {
		fmt.Fprintln(w, "per second, from the first arrival:")
		counts := map[int]int{}
		last := 0
		for _, at := range arrivals {
			sec := int(at.Sub(arrivals[0]) / time.Second)
			counts[sec]++
			last = max(last, sec)
		}
		for sec := 0; sec <= last; sec++ {
			fmt.Fprintf(w, "  %d  %d\n", sec, counts[sec])
		}
	}

	if len(holds) > 0 {
		sorted := slices.Clone(holds)
		slices.Sort(sorted)
		rank := func(p float64) time.Duration {
			return sorted[max(0, int(float64(len(sorted))*p+0.999999)-1)]
		}
		fmt.Fprintf(w, "held, answered calls: p50 %v  p99 %v  max %v\n", rank(0.50), rank(0.99), sorted[len(sorted)-1])
	}
}

func main() {
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
	}()

	err := run(os.Args[1:], os.Stdout, os.Stderr, stop, func(string) {})
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stand:", err)
		os.Exit(1)
	}
}
