package main

import (
	"os"
	"testing"
	"time"
)

// The end-to-end runs here check what the report says about the target on a
// loopback target answering in microseconds; on a 0.5 ms Windows clock every
// one would be invalid. The clock rule has its own tests.
func TestMain(m *testing.M) {
	clockStep = func() time.Duration { return 0 }
	os.Exit(m.Run())
}
