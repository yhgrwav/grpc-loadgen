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

// Command grpc-loadgen runs a load test described by a YAML config against a
// gRPC target, prints a report and exits non-zero when the run fails.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/yhgrwav/grpc-loadgen/internal/cli"
	"github.com/yhgrwav/grpc-loadgen/pkg/config"
	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "grpc-loadgen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("grpc-loadgen", flag.ContinueOnError)

	var (
		configPath  = flags.String("c", "", "path to the config file")
		maxInFlight = flags.Int("max-in-flight", 5000, "cap on requests waiting for a reply")
		fakeDelay   = flags.Duration("fake-delay", 25*time.Millisecond, "latency of the built-in fake target")
		fakeJitter  = flags.Duration("fake-jitter", 10*time.Millisecond, "random spread added to the fake latency")
		fakeFail    = flags.Float64("fake-fail-ratio", 0, "share of fake replies that fail, 0 to 1")
	)

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		flags.Usage()

		return errors.New("no config given, use -c")
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}

	eng, err := engine.New(engine.Options{
		Calls:       cli.CallsFromConfig(cfg),
		Sender:      engine.FakeSender{Delay: *fakeDelay, Jitter: *fakeJitter, FailRatio: *fakeFail},
		MaxInFlight: *maxInFlight,
		Warmup:      cfg.Load.Warmup,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	target := string(cfg.App.Address)

	var runErr error

	start := func() error {
		runErr = eng.Run(ctx)

		return runErr
	}

	if cli.Interactive() {
		program := cli.NewProgram(target, eng, start, cancel)
		if _, err := program.Run(); err != nil {
			return err
		}
	} else if err := cli.RunPlain(os.Stderr, target, eng, start); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	cli.PrintReport(os.Stdout, target, eng.Report())

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}

	return nil
}
