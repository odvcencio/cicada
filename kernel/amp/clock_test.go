//go:build linux || darwin

package amp

import (
	"runtime"
	"testing"
	"time"
)

func TestBlockClockExcludesDescheduledTime(t *testing.T) {
	t.Setenv("CICADA_WALLCLOCK_TIMING", "")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	start, wallStart := blockClock(), time.Now()
	time.Sleep(30 * time.Millisecond)
	elapsed, wallElapsed := blockClock()-start, time.Since(wallStart)
	if blockClockName() != "thread CPU time" || elapsed < 0 || elapsed >= wallElapsed/2 {
		t.Fatalf("descheduled time counted as work: cpu=%s wall=%s clock=%s", elapsed, wallElapsed, blockClockName())
	}
	start = blockClock()
	deadline := time.Now().Add(10 * time.Millisecond)
	for time.Now().Before(deadline) {
	}
	if elapsed := blockClock() - start; elapsed <= 0 {
		t.Fatalf("active thread work was not counted: %s", elapsed)
	}
}

func TestBlockClockRetainsExplicitWallTiming(t *testing.T) {
	t.Setenv("CICADA_WALLCLOCK_TIMING", "1")
	start := blockClock()
	time.Sleep(10 * time.Millisecond)
	if elapsed := blockClock() - start; elapsed < 10*time.Millisecond || blockClockName() != "wall clock" {
		t.Fatalf("explicit wall timing excluded elapsed time: elapsed=%s clock=%s", elapsed, blockClockName())
	}
}
