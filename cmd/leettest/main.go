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
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yhgrwav/leettest/internal/cli"
	"github.com/yhgrwav/leettest/pkg/clock"
	"github.com/yhgrwav/leettest/pkg/config"
	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
	"github.com/yhgrwav/leettest/pkg/grpcsender"
)

const defaultMaxInFlight = 5000

// ErrIncomplete says the run ended before its plan. The report is printed and
// honest, but it covers less than was asked for, so the exit code is not zero:
// a pipeline must not pass on a three-minute run of a ten-minute plan.
// ErrInvalidRun says the run's numbers say nothing about the target: the
// generator hit its own cap. The report prints the verdict; the code carries
// it to a pipeline.
var ErrInvalidRun = errors.New("the run is invalid: its numbers do not describe the target")

var ErrIncomplete = errors.New("the run stopped before its planned end; the report covers only the part that ran")

// exitNow is the way out that depends on nothing: the third stop, or an abort
// that has not finished in time.
var exitNow = func() {
	fmt.Fprintln(os.Stderr, "leettest: aborted without a report")
	sig, _ := lastSignal.Load().(os.Signal)
	os.Exit(abortCode(sig))
}

// lastSignal is what asked the run to stop, so an abort without a report
// exits the way that signal would have: a script reads 143 from an
// orchestrator's SIGTERM and 130 from a person's Ctrl+C.
var lastSignal atomic.Value

// runStarting is called once presses go to the stopper; tests use it to press
// during the run rather than during the connection.
var runStarting = func(*engine.Engine) {}

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	stops, aborts := make(chan struct{}), make(chan struct{})
	go func() {
		for sig := range signals {
			lastSignal.Store(sig)

			if sig == syscall.SIGTERM {
				aborts <- struct{}{}
			} else {
				stops <- struct{}{}
			}
		}
	}()

	err := run(context.Background(), stops, aborts, os.Args[1:], os.Stdout, os.Stderr)
	signal.Stop(signals)

	if err != nil && !errors.Is(err, flag.ErrHelp) {
		printError(os.Stderr, err)
	}

	os.Exit(exitCode(err))
}

// printError writes a failed run's error as it came: the final screen's short
// verdict sends the reader here for the details.
func printError(w io.Writer, err error) {
	fmt.Fprintf(w, "leettest: %v\n", err)
}

// exitCode maps the outcome onto codes a pipeline can tell apart. Whether the
// target is fast enough is not among them: that needs a threshold, and none is
// set yet, so a finished run exits 0 however the target answered.
//
//	0   the plan ran to its end and the report is complete
//	1   the run never started: bad flags, bad config, no connection
//	2   the run is invalid: its numbers do not describe the target
//	3   the run stopped before its planned end; the report covers less
//	130 aborted without a report after Ctrl+C
//	143 aborted without a report after SIGTERM
//
// The codes are ranked, not summed: a run that is both invalid and short
// reports 2, because numbers that do not describe the target are worse news
// than covering less of the plan. 130 and 143 are not outcomes of a run at
// all — they are the exit that prints nothing, so a stop that did print a
// report is 3 whichever key ended it.
func exitCode(err error) int {
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, ErrInvalidRun):
		return 2
	case errors.Is(err, ErrIncomplete):
		return 3
	default:
		return 1
	}
}

// abortCode is the shell's convention for a process killed by a signal:
// 128 plus the signal's number.
func abortCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}

	return 130
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
		showVersion    = flags.Bool("version", false, "print the version and exit")
		output         = flags.String("output", "text", "report format on stdout: text, or json for scripts")
	)

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		info, _ := debug.ReadBuildInfo()
		fmt.Fprintf(stdout, "leettest %s\n", versionString(version, info))

		return nil
	}
	if *configPath == "" {
		flags.Usage()

		return errors.New("no config given, use -c")
	}
	if *output != "text" && *output != "json" {
		return fmt.Errorf("-output %q: want text or json", *output)
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
		var senderOpts grpcsender.Options
		if senderOpts, err = senderOptions(&cfg.App); err != nil {
			return err
		}
		grpcSender = grpcsender.New(senderOpts)
		sender = grpcSender
	}

	// Raise, measure, run, measure, restore: the floor of "waited" is set by
	// the step before the run, the report prints the larger of the two.
	restoreTimer, timerRaised := clock.Raise()
	defer restoreTimer()
	stepBefore := clockStep()

	opts := engine.Options{
		Calls:       cli.CallsFromConfig(cfg),
		Sender:      sender,
		MaxInFlight: *maxInFlight,
		Warmup:      cfg.Load.Warmup,
		WaitFloor:   engine.WaitFloorFor(stepBefore),
	}

	// A config error does not wait for the network: checked before connecting.
	if err = engine.CheckOptions(opts); err != nil {
		return withBudgetAdvice(err, opts)
	}

	// Methods nothing could be checked against: named again with the report,
	// where a full-screen run does not scroll them away.
	var unchecked []cli.Unchecked

	if grpcSender != nil {
		defer func() { _ = grpcSender.Close() }()

		if connErr := connect(ctx, stderr, grpcSender, &cfg.App, *connectTimeout); connErr != nil {
			return connErr
		}

		// Bodies need the schema, and the schema needs the connection.
		dataCtx, cancelData := context.WithTimeout(ctx, *connectTimeout)
		var dataErr error
		unchecked, dataErr = cli.AttachData(dataCtx, descriptor.NewReflectionResolver(grpcSender.Conn()), cfg, opts.Calls)
		cancelData()

		if dataErr != nil {
			return dataErr
		}
		for _, m := range unchecked {
			fmt.Fprintf(stderr, "%s: %v; the method was not checked before the run and sends an "+
				"empty message\n", m.Method, m.Err)
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

	var stepAfter atomic.Int64
	start := func() error {
		err := eng.Run(ctx)
		stepAfter.Store(int64(clockStep()))

		return err
	}

	// One report for the final screen and for stdout.
	var maxResponse string
	if raw := cfg.App.RawMaxResponseSize; raw != nil {
		maxResponse = strings.TrimSpace(*raw)
	}
	reportOf := func() cli.RunReport {
		return cli.RunReport{
			Report: eng.Report(), Unchecked: unchecked, MaxResponse: maxResponse,
			ClockStep: max(stepBefore, time.Duration(stepAfter.Load())), ClockStepBefore: stepBefore,
			TimerNotRaised: !timerRaised,
		}
	}

	var runErr error

	if interactive {
		program := cli.NewProgram(target, cli.ServiceLabel(cfg, *configPath), eng, cfg.Load.Warmup, settings, s, reportOf)
		view.Store(program)
		runErr = cli.RunLive(program, s, start, abort)
	} else if runErr = cli.RunPlain(stderr, target, eng, start); runErr != nil &&
		!errors.Is(runErr, context.Canceled) && !errors.Is(runErr, engine.ErrInFlightCapExceeded) {
		return runErr
	}

	report := reportOf()
	result := runResult(report, runErr)
	if *output == "json" {
		// Only a run that happened has a report: on exit 1 stdout stays empty,
		// and a script reads the exit code first.
		if outcome, ok := outcomeOf(result); ok {
			info, _ := debug.ReadBuildInfo()
			if err := cli.WriteJSON(stdout, cli.JSONRun{
				Target: target, Version: versionString(version, info), Outcome: outcome,
				StartedAt: report.StartedAt, Run: report,
			}); err != nil {
				return fmt.Errorf("write the JSON report: %w", err)
			}
		}
	} else {
		cli.PrintReport(stdout, target, report)
	}
	s.Finish()

	return result
}

// outcomeOf names a run's result in the JSON report, the same as its exit code
// says: 0 complete, 2 invalid, 3 incomplete. Any other error has no report.
func outcomeOf(result error) (string, bool) {
	switch exitCode(result) {
	case 0:
		return cli.OutcomeComplete, true
	case 2:
		return cli.OutcomeInvalid, true
	case 3:
		return cli.OutcomeIncomplete, true
	default:
		return "", false
	}
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
// them. The engine knows neither the config fields nor the flags. opts are
// the ones err came from.
func withBudgetAdvice(err error, opts engine.Options) error {
	var budget *engine.InFlightBudgetError
	if !errors.As(err, &budget) || len(budget.Unbounded) > 0 || budget.PeakRPS == 0 {
		return err
	}

	// The formula only bounds the answer from above: each call rounds its own
	// rps × timeout up. The check itself says which timeout fits, so the
	// advice is the largest one it accepts, to the millisecond unless that
	// would round it to zero.
	passes := func(timeout time.Duration) bool {
		trial := opts
		trial.Calls = slices.Clone(opts.Calls)
		for i := range trial.Calls {
			trial.Calls[i].Timeout = timeout
		}

		return !errors.Is(engine.CheckOptions(trial), engine.ErrInFlightBudget)
	}

	upper := time.Duration(budget.Cap-budget.Reserved) * time.Second / time.Duration(budget.PeakRPS)
	step := time.Millisecond
	if upper < 2*time.Millisecond {
		step = time.Microsecond
	}
	fits := upper.Truncate(step) + step
	for fits > 0 && !passes(fits) {
		fits -= step
	}
	if fits <= 0 {
		return fmt.Errorf("%w\nno timeout fits this cap: run with -max-in-flight %d", err, budget.Need)
	}

	return fmt.Errorf("%w\nset timeout to at most %s for every call, or run with -max-in-flight %d",
		err, fits, budget.Need)
}

// senderOptions reads the certificate files the config names, before any
// connection: a wrong path is a config error, not a failed handshake.
func senderOptions(app *config.App) (grpcsender.Options, error) {
	opts := grpcsender.Options{
		Target:           string(app.Address),
		TLS:              app.UseTLS,
		Metadata:         app.Metadata,
		ServerName:       app.ServerName,
		MaxResponseBytes: app.MaxResponseBytes,
	}

	if app.CA != "" {
		raw, err := os.ReadFile(app.CA)
		if err != nil {
			return opts, fmt.Errorf("app.ca: %w", err)
		}

		opts.RootCAs = x509.NewCertPool()
		if !opts.RootCAs.AppendCertsFromPEM(raw) {
			return opts, fmt.Errorf("app.ca: %s holds no PEM certificate", app.CA)
		}
	}

	if app.Cert != "" {
		if err := refuseEncryptedKey(app.Key); err != nil {
			return opts, err
		}

		cert, err := tls.LoadX509KeyPair(app.Cert, app.Key)
		if err != nil {
			return opts, fmt.Errorf("app.cert %s, app.key %s: %w", app.Cert, app.Key, err)
		}
		opts.Certificates = []tls.Certificate{cert}
	}

	return opts, nil
}

// refuseEncryptedKey names a password-protected key for what it is; the
// parser would only say it cannot parse it. A file it cannot read is left to
// the loader, which names it.
func refuseEncryptedKey(path string) error {
	raw, _ := os.ReadFile(path)

	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		if strings.Contains(block.Type, "ENCRYPTED") || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
			return fmt.Errorf("app.key %s: encrypted keys are not supported; decrypt it first", path)
		}
	}

	return nil
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

	text := ""
	if err != nil {
		text = err.Error()
	}

	// The hints read the errors' text: crypto/tls and grpc-go give these
	// cases no type of their own that survives the connection error.
	switch {
	case err == nil:
		return nil
	case errors.Is(err, grpcsender.ErrClosedAfterHandshake):
		return fmt.Errorf("%w (%s)", err, mode)
	case app.UseTLS && (strings.Contains(text, "certificate is valid for") || strings.Contains(text, "doesn't contain any IP SANs")):
		return fmt.Errorf("%w\nThe target's certificate does not name %s; if it names another host, set app.server_name to it",
			err, app.Address)
	case app.UseTLS && strings.Contains(text, "does not look like a TLS handshake"):
		return fmt.Errorf("%w\nThe target does not speak TLS; set app.tls: false", err)
	case !app.UseTLS && strings.Contains(text, "server preface"):
		return fmt.Errorf("%w\nTLS is off (app.tls: false), and the target closed the connection; if it uses TLS, set app.tls: true", err)
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

// runResult is what run returns once the report is printed. A cap hit is not
// an error: the report printed its verdict, and the run is incomplete.
func runResult(run cli.RunReport, runErr error) error {
	report := run.Report
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, engine.ErrInFlightCapExceeded) {
		return runErr
	}
	if report.CapHit != nil || report.RequestRejected || cli.ClockTooCoarse(run) {
		return ErrInvalidRun
	}
	if report.Incomplete {
		return ErrIncomplete
	}

	return nil
}

// clockStep measures the host clock; the end-to-end tests fix it so that a
// coarse developer clock does not turn the runs they check into invalid ones.
var clockStep = func() time.Duration { return clock.StepOf(time.Now) }
