// cicada-sample-metrics measures the standalone stereo sample pool. Run with
// GOWORK=off nice -n 10 under the shared local-heavy.lock. Clock-read overhead
// is included; a GC occurs before each warmup, never in the timed loop by design.
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"m31labs.dev/cicada/kernel/voice/sample"
)

func main() {
	runtime.GOMAXPROCS(1)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cpu := "unavailable"
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "model name") {
				_, name, _ := strings.Cut(line, ":")
				cpu = strings.TrimSpace(name)
				break
			}
		}
	}
	fmt.Printf("METRIC MACHINE cpu=%q os=%s arch=%s go=%s gomaxprocs=1 stereo=true block_frames=128 rate_hz=48000 clock_overhead=included\n", cpu, runtime.GOOS, runtime.GOARCH, runtime.Version())
	const frames, blocks = 128, 10000
	region := sample.Region{Left: make([]float32, 65536), Right: make([]float32, 65536), SampleRate: 48000, RootKey: 60, End: 65536, Loop: true, LoopEnd: 65536}
	for i := range region.Left {
		region.Left[i] = float32(.3 * math.Sin(float64(i)*.03))
		region.Right[i] = float32(.3 * math.Cos(float64(i)*.037))
	}
	var left, right [frames]float32
	elapsed := make([]int64, blocks)
	for _, ratio := range []float64{1, 44100.0 / 48000, 1.5, 4} {
		for _, count := range []int{1, 8, 32, 64} {
			pool, err := sample.NewPool(48000, count, region)
			must(err)
			must(pool.SetParams(sample.Params{Gain: 1, FineTuneCents: 1200 * math.Log2(ratio)}))
			for i := 0; i < count; i++ {
				_, err := pool.NoteOn(60, 127)
				must(err)
			}
			voice, err := sample.New(48000, region)
			must(err)
			must(voice.SetParams(sample.Params{Gain: 1, FineTuneCents: 1200 * math.Log2(ratio)}))
			must(voice.NoteOn(60, 127))
			runtime.GC()
			for i := 0; i < 1000; i++ {
				pool.Render(left[:], right[:])
			}
			for i := range elapsed {
				start := time.Now()
				pool.Render(left[:], right[:])
				elapsed[i] = time.Since(start).Nanoseconds()
			}
			sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
			// Nearest-rank percentiles over 10,000 individual block durations.
			p50, p99 := elapsed[blocks/2-1], elapsed[blocks*99/100-1]
			quantum := float64(frames) / 48000 * 1e9
			fmt.Printf("METRIC CPU voices=%d ratio=%.9f taps=%d blocks=%d p50_ns=%d p99_ns=%d per_voice_p50_ns=%.1f per_voice_p99_ns=%.1f quantum_p50_pct=%.3f quantum_p99_pct=%.3f\n",
				count, voice.Ratio(), voice.KernelTaps(), blocks, p50, p99, float64(p50)/float64(count), float64(p99)/float64(count), 100*float64(p50)/quantum, 100*float64(p99)/quantum)
		}
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
