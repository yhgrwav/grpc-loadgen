//go:build !windows

package clock

// Linux and macOS clocks step in nanoseconds: there is no timer to raise.
var hostTimer = timerAPI{}
