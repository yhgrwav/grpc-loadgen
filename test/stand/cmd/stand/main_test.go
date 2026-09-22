package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestParse_RejectsTwoBehaviorsAtOnce(t *testing.T) {
	_, err := parse([]string{"-freeze-for", "1s", "-hang-from", "8s"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "-freeze-for") || !strings.Contains(err.Error(), "-hang-from") {
		t.Fatalf("err = %v, want one naming both flags", err)
	}
}

func TestParse_RejectsFreezeAtWithoutLength(t *testing.T) {
	if _, err := parse([]string{"-freeze-at", "5s"}, io.Discard); err == nil {
		t.Fatal("want an error: a freeze start without a length freezes nothing")
	}
}

func TestParse_RejectsNegativeValues(t *testing.T) {
	for _, args := range [][]string{
		{"-delay", "-1ms"},
		{"-freeze-at", "-1s", "-freeze-for", "1s"},
		{"-hang-from", "-1s"},
		{"-fail-every", "-2"},
		{"-life", "-1s"},
	} {
		if _, err := parse(args, io.Discard); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

func TestRun_CountsWhatArrivedAndWasAnswered(t *testing.T) {
	var out, errOut bytes.Buffer
	stop := make(chan struct{})
	addr := make(chan string, 1)
	done := make(chan error, 1)

	go func() {
		done <- run([]string{"-addr", "127.0.0.1:0"}, &out, &errOut, stop, func(a string) { addr <- a })
	}()

	var target string
	select {
	case target = <-addr:
	case <-time.After(5 * time.Second):
		t.Fatal("stand did not start")
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := grpc_health_v1.NewHealthClient(conn).Check(t.Context(), &grpc_health_v1.HealthCheckRequest{}); err != nil {
		t.Fatalf("check: %v", err)
	}

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after stop")
	}

	for _, want := range []string{"arrivals 1, answered 1", "\n  0  1\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
}
