//go:build darwin && (amd64 || arm64)

package midi

import "golang.org/x/sys/unix"

// HostTimeNanos reads the monotonic uptime clock used by CoreMIDI timestamps,
// expressed in nanoseconds. Wall-clock corrections do not move this clock.
func HostTimeNanos() int64 {
	var now unix.Timespec
	if unix.ClockGettime(unix.CLOCK_UPTIME_RAW, &now) != nil {
		return 0
	}
	return now.Nano()
}
