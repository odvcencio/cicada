//go:build lanestress

package renderlanes

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"
)

// StressEnabled reports whether this build has the lane stress hook
// (the lanestress build tag).
const StressEnabled = true

// stressSeed seeds the hook: CICADA_LANE_STRESS_SEED, or the clock. It is
// set once, before any test runs, and only read afterwards.
var stressSeed = func() uint64 {
	if text := os.Getenv("CICADA_LANE_STRESS_SEED"); text != "" {
		seed, err := strconv.ParseUint(text, 10, 64)
		if err == nil {
			return seed
		}
		fmt.Fprintf(os.Stderr, "renderlanes: CICADA_LANE_STRESS_SEED=%q is not a number; using the clock\n", text)
	}
	return uint64(time.Now().UnixNano())
}()

// Every stress run prints its seed; CICADA_LANE_STRESS_SEED sets it again.
func init() {
	fmt.Fprintf(os.Stderr, "renderlanes: lane stress seed %d (CICADA_LANE_STRESS_SEED gives each hand-off the same delay again)\n", stressSeed)
}

// StressSeed is the seed of this process's stress hook; a failing test
// prints it.
func StressSeed() uint64 { return stressSeed }

// stressLong is the long delay: five times as long as a lane spins
// (spinWait) before it parks, so one long delay decides the order of its
// hand-off by itself, whatever the machine's own timing.
const stressLong = 5 * spinWait

// StressPoint, at a hand-off point of the lane protocol, sleeps stressLong at
// about 1 in 32 hand-offs, yields the processor at about 1 in 4 of the rest,
// and otherwise does nothing. The choice is a hash of the seed, the site and
// two values the caller holds (a segment number and a lane, say), so a seed
// gives each hand-off the same delay in every run. There are no short random
// sleeps: a few tens of microseconds is within a lane's spin window, so
// whether a lane was spinning or parked would depend on the machine and a
// seed would not fail at the same segment twice.
//
// The hook reads nothing another goroutine writes: a shared counter or lock
// here would order the goroutines it perturbs, and the race detector would
// stop reporting races across the hand-off.
func StressPoint(site int, a, b int64) {
	x := stressSeed ^ uint64(site)*0x9e3779b97f4a7c15 ^ uint64(a)*0xbf58476d1ce4e5b9 ^ uint64(b)*0x94d049bb133111eb
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	x ^= x >> 31
	if x%32 == 0 {
		time.Sleep(stressLong)
		return
	}
	if x>>6%4 == 0 {
		runtime.Gosched()
	}
}
