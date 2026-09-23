package stand_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yhgrwav/leettest/test/stand"
)

func startTCP(t *testing.T, answer stand.Answer) *stand.Stand {
	t.Helper()

	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := stand.StartOn(lis, answer)
	t.Cleanup(s.Stop)

	return s
}

// Another tool, or our own binary, reaches the stand only over the network.
func TestStand_ServesOverTCP(t *testing.T) {
	s := startTCP(t, stand.Constant(0))

	if _, err := check(t.Context(), t, dial(t, s)); err != nil {
		t.Fatalf("check over TCP: %v", err)
	}
	if got := len(s.Arrivals()); got != 1 {
		t.Errorf("arrivals = %d, want 1", got)
	}
}

func TestStand_TCPTargetIsTheListenAddress(t *testing.T) {
	lis, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := stand.StartOn(lis, nil)
	t.Cleanup(s.Stop)

	if got, want := s.Target(), lis.Addr().String(); got != want {
		t.Errorf("Target = %q, want %q", got, want)
	}
}

func TestHangingFrom_AnswersBeforeAndHangsFrom(t *testing.T) {
	answer := stand.HangingFrom(8*time.Second, 20*time.Millisecond)

	tests := []struct {
		since time.Duration
		want  stand.Behavior
	}{
		{since: 0, want: stand.Behavior{Delay: 20 * time.Millisecond}},
		{since: 8*time.Second - time.Nanosecond, want: stand.Behavior{Delay: 20 * time.Millisecond}},
		{since: 8 * time.Second, want: stand.Behavior{Hang: true}},
		{since: time.Hour, want: stand.Behavior{Hang: true}},
	}
	for _, tt := range tests {
		if got := answer(stand.Call{N: 1, Since: tt.since}); got != tt.want {
			t.Errorf("since %v: behavior = %+v, want %+v", tt.since, got, tt.want)
		}
	}
}

// A stand's own record of how long it held each answer is the truth a report's
// latency is checked against, from outside the generator.
func TestStand_HoldsRecordsOnlyAnsweredCalls(t *testing.T) {
	const delay = 60 * time.Millisecond

	s := startTCP(t, func(c stand.Call) stand.Behavior {
		switch c.N {
		case 1:
			return stand.Behavior{Delay: delay}
		case 2:
			return stand.Behavior{Delay: delay, Code: codes.ResourceExhausted}
		default:
			return stand.Behavior{Hang: true}
		}
	})
	client := dial(t, s)

	if _, err := check(t.Context(), t, client); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := check(t.Context(), t, client); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second call: %v, want ResourceExhausted", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := check(ctx, t, client); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("third call: %v, want DeadlineExceeded", err)
	}

	holds := s.Holds()
	if len(holds) != 2 {
		t.Fatalf("holds = %v, want two: a refusal is an answer, a hung call is not", holds)
	}
	for i, h := range holds {
		if h < delay || h > delay+time.Second {
			t.Errorf("hold %d = %v, want about %v", i, h, delay)
		}
	}
}
