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

// Command leettest runs a load test described by a YAML config against a
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/leettest/internal/cli"
	"github.com/yhgrwav/leettest/pkg/config"
	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/grpcsender"
)

const defaultMaxInFlight = 5000

// ErrIncomplete says the run ended before its plan. The report is printed and
// honest, but it covers less than was asked for, so the exit code is not zero:
// a pipeline must not pass on a three-minute run of a ten-minute plan.
var ErrIncomplete = errors.New("the run stopped before its planned end; the report covers only the part that ran")

// exitNow is the way out that depends on nothing: the third stop, or an abort
// that has not finished in time.
var exitNow = func() {
	fmt.Fprintln(os.Stderr, "leettest: aborted without a report")
	os.Exit(130)
}

// runStarting is called once presses go to the stopper; tests use it to press
// during the run rather than during the connection.
var runStarting = func(*engine.Engine) {}

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	stops, aborts := make(chan struct{}), make(chan struct{})
	go func() {
		for sig := range signals {
			if sig == syscall.SIGTERM {
				aborts <- struct{}{}
			} else {
				stops <- struct{}{}
			}
		}
	}()

	err := run(context.Background(), stops, aborts, os.Args[1:], os.Stdout, os.Stderr)
	signal.Stop(signals)

	if err != nil {
		fmt.Fprintf(os.Stderr, "leettest: %v\n", err)
		os.Exit(1)
	}
}

// run is the whole command. The live view is used only when stderr is the
// process terminal, so tests passing their own writers always get plain output.
//
// Each value on stops is one press of Ctrl+C, each value on aborts one SIGTERM.
// Before the run starts either cancels the connection; during the run they go
// to the three-stage stopper.
func run(ctx context.Context, stops, aborts <-chan struct{}, args []string, stdout, stderr io.Writer) error {
	ctx, abort := context.WithCancel(ctx)
	defer abort()

	var stopper atomic.Pointer[cli.Stopper]
	// SIGTERM comes from an orchestrator, not a keyboard: no Ctrl+C hint then.
	var terminated atomic.Bool

	finished := make(chan struct{})
	defer close(finished)

	go func() {
		for {
			select {
			case <-finished:
				return
			case <-stops:
				if s := stopper.Load(); s != nil {
					s.Press()
				} else {
					abort()
				}
			case <-aborts:
				terminated.Store(true)
				if s := stopper.Load(); s != nil {
					s.Abort()
				} else {
					abort()
				}
			}
		}
	}()

	flags := flag.NewFlagSet("leettest", flag.ContinueOnError)
	flags.SetOutput(stderr)

	interactive := stderr == io.Writer(os.Stderr) && cli.Interactive()
	// The stopper writes from the signal goroutine while run writes too.
	stderr = &lockedWriter{w: stderr}

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

	opts := engine.Options{
		Calls:       cli.CallsFromConfig(cfg),
		Sender:      sender,
		MaxInFlight: *maxInFlight,
		Warmup:      cfg.Load.Warmup,
	}

	// A config error does not wait for the network: checked before connecting.
	if err = engine.CheckOptions(opts); err != nil {
		return withBudgetAdvice(err)
	}

	if grpcSender != nil {
		defer func() { _ = grpcSender.Close() }()

		if connErr := connect(ctx, stderr, grpcSender, &cfg.App, *connectTimeout); connErr != nil {
			return connErr
		}

		// Bodies need the schema, and the schema needs the connection.
		dataCtx, cancelData := context.WithTimeout(ctx, *connectTimeout)
		dataErr := cli.AttachData(dataCtx, descriptor.NewReflectionResolver(grpcSender.Conn()), cfg, opts.Calls)
		cancelData()

		if dataErr != nil {
			return dataErr
		}
	}

	eng, err := engine.New(opts)
	if err != nil {
		return err
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

	// The live view holds the terminal in raw mode; leaving without restoring it
	// would leave the user a broken console.
	var view atomic.Pointer[tea.Program]
	exit := func() {
		if p := view.Load(); p != nil {
			p.Kill()
		}
		exitNow()
	}

	s := cli.NewStopper(
		// Each message goes out before its action: once the action lets run
		// return, nothing may write to stderr any more.
		func() {
			if !interactive {
				fmt.Fprintln(stderr, "stopping: no new requests; waiting for those in flight. Ctrl+C again to cut them off")
			}
			eng.Stop()
		},
		func() {
			if !interactive {
				fmt.Fprint(stderr, "aborting: requests in flight are cut off and counted as aborted")
				if !terminated.Load() {
					fmt.Fprint(stderr, ". Ctrl+C again to exit without a report")
				}
				fmt.Fprintln(stderr)
			}
			abort()
		},
		exit,
		time.Second,
	)
	stopper.Store(s)
	runStarting(eng)

	start := func() error { return eng.Run(ctx) }

	var runErr error

	if interactive {
		program := cli.NewProgram(target, cli.ServiceLabel(cfg, *configPath), eng, cfg.Load.Warmup, settings, s)
		view.Store(program)
		runErr = cli.RunLive(program, start, abort)
	} else if runErr = cli.RunPlain(stderr, target, eng, start); runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}

	report := eng.Report()
	cli.PrintReport(stdout, target, report)
	s.Finish()

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	if report.Incomplete {
		return ErrIncomplete
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

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(p)
}
