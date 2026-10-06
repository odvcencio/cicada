//go:build linux

package amp

import (
	"syscall"
	"time"
	"unsafe"
)

const clockThreadCPUTimeID = 3 // CLOCK_THREAD_CPUTIME_ID

// blockClock reads the calling thread's CPU time, so time the scheduler gives
// to other processes does not count against the block budget. Callers lock
// the goroutine to its OS thread first.
func blockClock() time.Duration {
	if wallClockTiming() {
		return time.Duration(time.Now().UnixNano())
	}
	var ts syscall.Timespec
	if _, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockThreadCPUTimeID, uintptr(unsafe.Pointer(&ts)), 0); errno != 0 {
		return time.Duration(time.Now().UnixNano())
	}
	return time.Duration(ts.Nano())
}

func blockClockName() string {
	if wallClockTiming() {
		return "wall clock"
	}
	return "thread CPU time"
}
