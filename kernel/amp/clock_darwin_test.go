//go:build darwin

package amp

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func blockClock() time.Duration {
	if wallClockTiming() {
		return time.Duration(time.Now().UnixNano())
	}
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		panic(fmt.Sprintf("read audio test thread CPU clock: %v", err))
	}
	return time.Duration(ts.Nano())
}

func blockClockName() string {
	if wallClockTiming() {
		return "wall clock"
	}
	return "thread CPU time"
}
