package clock

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	ntdll = syscall.NewLazyDLL("ntdll.dll")
	winmm = syscall.NewLazyDLL("winmm.dll")

	ntQueryTimerResolution = ntdll.NewProc("NtQueryTimerResolution")
	ntSetTimerResolution   = ntdll.NewProc("NtSetTimerResolution")
	timeBeginPeriod        = winmm.NewProc("timeBeginPeriod")
	timeEndPeriod          = winmm.NewProc("timeEndPeriod")
)

// Windows stamps time.Now on the system timer's interrupt, whose period is the
// finest any process asked for: 15.625 ms when none did.
var hostTimer = timerAPI{
	finest: func() (uint32, error) {
		var coarsest, finest, current uint32
		if err := ntdll.Load(); err != nil {
			return 0, err
		}
		status, _, _ := ntQueryTimerResolution.Call(
			uintptr(unsafe.Pointer(&coarsest)), uintptr(unsafe.Pointer(&finest)), uintptr(unsafe.Pointer(&current)))
		if status != 0 {
			return 0, fmt.Errorf("NtQueryTimerResolution: status %#x", status)
		}

		return finest, nil
	},
	set: func(period uint32, on bool) error {
		var current uint32
		var set uintptr
		if on {
			set = 1
		}
		status, _, _ := ntSetTimerResolution.Call(uintptr(period), set, uintptr(unsafe.Pointer(&current)))
		if status != 0 {
			return fmt.Errorf("NtSetTimerResolution: status %#x", status)
		}

		return nil
	},
	begin: func(ms uint32) error {
		if err := winmm.Load(); err != nil {
			return err
		}
		if r, _, _ := timeBeginPeriod.Call(uintptr(ms)); r != 0 {
			return fmt.Errorf("timeBeginPeriod: %d", r)
		}

		return nil
	},
	end: func(ms uint32) error {
		if r, _, _ := timeEndPeriod.Call(uintptr(ms)); r != 0 {
			return fmt.Errorf("timeEndPeriod: %d", r)
		}

		return nil
	},
}
