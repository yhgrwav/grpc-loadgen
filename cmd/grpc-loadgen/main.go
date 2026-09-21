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
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/yhgrwav/grpc-loadgen/internal/cli"
	"github.com/yhgrwav/grpc-loadgen/pkg/config"
	"github.com/yhgrwav/grpc-loadgen/pkg/engine"
	"github.com/yhgrwav/grpc-loadgen/pkg/grpcsender"
)

const defaultMaxInFlight = 5000

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()

	if err != nil {
		fmt.Fprintf(os.Stderr, "grpc-loadgen: %v\n", err)
		os.Exit(1)
	}
}

// run is the whole command. The live view is used only when stderr is the
// process terminal, so tests passing their own writers always get plain output.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("grpc-loadgen", flag.ContinueOnError)
	flags.SetOutput(stderr)

	interactive := stderr == io.Writer(os.Stderr) && cli.Interactive()

	var (
		configPath     = flags.String("c", "", "path to the config file")
		maxInFlight    = flags.Int("max-in-flight", defaultMaxInFlight, "cap on requests waiting for a reply")
		connectTimeout = flags.Duration("connect-timeout", 10*time.Second, "how long to wait for a target that accepts the connection but does not answer")
		fake           = flags.Bool("fake", false, "load the built-in fake target instead of the one in the config")
		fakeDelay      = flags.Duration("fake-delay", 25*time.Millisecond, "latency of the fake target, with -fake")
		fakeJitter     = flags.Duration("fake-jitter", 10*time.Millisecond, "random spread added to the fake latency, with -fake")
		fakeFail       = flags.Float64("fake-fail-ratio", 0, "share of fake replies that fail, 0 to 1, with -fake")
	)

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		flags.Usage()

		return errors.New("no config given, use -c")
	}
	if err := checkFakeFlags(flags, *fake); err != nil {
		return err
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}

	target := string(cfg.App.Address)

	var (
		sender     engine.Sender
		grpcSender *grpcsender.Sender
	)

	if *fake {
		target = cli.FakeTarget
		sender = engine.FakeSender{Delay: *fakeDelay, Jitter: *fakeJitter, FailRatio: *fakeFail}
	} else {
		grpcSender = grpcsender.New(grpcsender.Options{Target: target, TLS: cfg.App.UseTLS})
		sender = grpcSender
	}

	eng, err := engine.New(engine.Options{
		Calls:       cli.CallsFromConfig(cfg),
		Sender:      sender,
		MaxInFlight: *maxInFlight,
		Warmup:      cfg.Load.Warmup,
	})
	if err != nil {
		return withBudgetAdvice(err)
	}

	if grpcSender != nil {
		defer func() { _ = grpcSender.Close() }()

		if connErr := connect(ctx, stderr, grpcSender, &cfg.App, *connectTimeout); connErr != nil {
			return connErr
		}
	}

	settings, err := cli.LoadSettings()
	if err != nil {
		return err
	}

	if !settings.Configured() {
		if interactive {
			if err := cli.RunSetup(settings); err != nil {
				return err
			}
		} else {
			settings.Lang = string(cli.DetectLang())
			settings.Mode = string(cli.ModeDark)
			settings.Palette = cli.Palettes()[0].Name
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	start := func() error { return eng.Run(ctx) }

	var runErr error

	if interactive {
		program := cli.NewProgram(target, eng, cfg.Load.Warmup, settings, cancel)
		runErr = cli.RunLive(program, start, cancel)
	} else if runErr = cli.RunPlain(stderr, target, eng, start); runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}

	cli.PrintReport(stdout, target, eng.Report())

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}

	return nil
}

// checkFakeFlags rejects tuning of the fake target when it is not in use: the
// flags would change nothing, and nothing would say so.
func checkFakeFlags(flags *flag.FlagSet, fake bool) error {
	if fake {
		return nil
	}

	var err error

	flags.Visit(func(f *flag.Flag) {
		if err == nil && strings.HasPrefix(f.Name, "fake-") {
			err = fmt.Errorf("-%s tunes the fake target and does nothing without -fake", f.Name)
		}
	})

	return err
}

// withBudgetAdvice turns the engine's numbers into the two settings that fix
// them. The engine knows neither the config fields nor the flags.
func withBudgetAdvice(err error) error {
	var budget *engine.InFlightBudgetError
	if !errors.As(err, &budget) || len(budget.Unbounded) > 0 || budget.PeakRPS == 0 {
		return err
	}

	// Rounded down, so the advice still fits; to the millisecond unless that
	// would round it to zero.
	fits := time.Duration(budget.Cap) * time.Second / time.Duration(budget.PeakRPS)
	if fits >= time.Millisecond {
		fits = fits.Truncate(time.Millisecond)
	} else {
		fits = fits.Truncate(time.Microsecond)
	}

	return fmt.Errorf("%w\nset timeout to at most %s for every call, or run with -max-in-flight %d",
		err, fits, budget.Need)
}

// connect reaches the target before the run, so an unreachable one is an error
// with its address rather than a report full of failures.
func connect(ctx context.Context, stderr io.Writer, sender *grpcsender.Sender, app *config.App,
	timeout time.Duration,
) error {
	mode := "without TLS"
	if app.UseTLS {
		mode = "over TLS"
	}

	fmt.Fprintf(stderr, "connecting to %s %s ...\n", app.Address, mode)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := sender.Connect(ctx)

	switch {
	case err == nil:
		return nil
	case app.UseTLS && app.TLS == nil:
		// The commonest first-run failure: a plaintext local service and TLS on
		// by default, which the handshake error alone does not explain.
		return fmt.Errorf("%w\nTLS is on because app.tls is not set; for a plaintext server set app.tls: false", err)
	default:
		return fmt.Errorf("%w (%s)", err, mode)
	}
}
