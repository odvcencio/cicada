//go:build lanestress

package renderlanes

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

// StressEnabled reports whether this build has the lane stress hook
// (the lanestress build tag).
const StressEnabled = true

// stressSeed seeds the hook: CICADA_LANE_STRESS_SEED, or the clock.
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

// Every stress run prints its seed, so a failure in any package's tests can
// be replayed with CICADA_LANE_STRESS_SEED.
func init() {
	fmt.Fprintf(os.Stderr, "renderlanes: lane stress seed %d (CICADA_LANE_STRESS_SEED replays it)\n", stressSeed)
}

var stressCalls atomic.Uint64

// StressSeed is the seed of this process's stress hook; a failing test
// prints it, and CICADA_LANE_STRESS_SEED replays it.
func StressSeed() uint64 { return stressSeed }

// StressPoint, at the hand-off points of the lane protocol, does nothing,
// yields the processor, or sleeps 0 to 50 µs, chosen by a seeded hash of a
// process-wide call count, so tests explore interleavings that a quiet
// machine rarely produces.
func StressPoint() {
	x := stressSeed + stressCalls.Add(1)*0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	x ^= x >> 31
	switch x % 4 {
	case 0:
	case 1:
		runtime.Gosched()
	default:
		time.Sleep(time.Duration(x>>8%50_000) * time.Nanosecond)
	}
}
