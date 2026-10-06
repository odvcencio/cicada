//go:build !linux

package amp

import "time"

// blockClock uses the wall clock where per-thread CPU time is unavailable.
func blockClock() time.Duration { return time.Duration(time.Now().UnixNano()) }

func blockClockName() string { return "wall clock" }
