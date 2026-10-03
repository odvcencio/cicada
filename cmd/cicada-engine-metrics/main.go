// cicada-engine-metrics records single-core scaling baselines without changing
// either engine. Construction and formatting are outside the callback timing.
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"m31labs.dev/cicada/render"
)

type options struct {
	blocks, runs, warmup, bars int
	filter                     string
	offline                    bool
}

func main() {
	o := options{}
	flag.IntVar(&o.blocks, "blocks", 1024, "timed blocks per run (must cross a bar for scene rows)")
	flag.IntVar(&o.runs, "runs", 3, "independent runs per row")
	flag.IntVar(&o.warmup, "warmup", 128, "untimed warmup blocks per native run")
	flag.IntVar(&o.bars, "bars", 2, "bars per offline WAV call (2 to 256)")
	flag.StringVar(&o.filter, "filter", "", "comma-separated substrings that must all occur in the scenario key")
	flag.BoolVar(&o.offline, "offline", true, "also time complete render.WAV calls")
	flag.Parse()
	if err := o.validate(); err != nil {
		fail(err)
	}
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
	fmt.Printf("METRIC MACHINE cpu=%q os=%s arch=%s go=%s gomaxprocs=1 stereo=true seed=%d clock_overhead=included native_gc=disabled offline_gc=enabled\n", cpu, runtime.GOOS, runtime.GOARCH, runtime.Version(), seed)
	matched := 0
	for _, s := range scenarios() {
		if !o.matches(s) {
			continue
		}
		matched++
		if err := measure(s, o, os.Stdout); err != nil {
			fail(fmt.Errorf("%s: %w", s.key(), err))
		}
	}
	if matched == 0 {
		fail(fmt.Errorf("filter matched no scenarios"))
	}
}

func (o options) validate() error {
	if o.blocks < 1 || o.runs < 1 || o.warmup < 1 || o.bars < 2 || o.bars > 256 || o.runs > math.MaxInt/o.blocks {
		return fmt.Errorf("blocks, runs and warmup must be positive; bars must be 2 to 256; sample count must fit int")
	}
	for _, s := range scenarios() {
		if o.matches(s) && s.scenes && int64(o.blocks)*int64(s.block) <= int64(s.rate)*2 {
			return fmt.Errorf("scene rows require blocks*block_frames > rate_hz*2 to cross a bar during timing")
		}
	}
	return nil
}

func (o options) matches(s scenario) bool {
	for _, part := range strings.Split(o.filter, ",") {
		if !strings.Contains(s.key(), part) {
			return false
		}
	}
	return true
}

// nearestRank returns the observed nearest-rank percentile, without interpolation.
func nearestRank(sorted []int64, percent int) int64 {
	return sorted[(len(sorted)*percent+99)/100-1]
}

func measure(s scenario, o options, output io.Writer) error {
	var factory func() (renderer, error)
	var offline func() (render.Report, error)
	path := "engine"
	if s.sampler() {
		path = "sampler_standalone"
		region := syntheticRegion(s.rate)
		factory = func() (renderer, error) { return newSampler(s, region) }
	} else {
		score, cfg, err := s.score()
		if err != nil {
			return err
		}
		factory = func() (renderer, error) { return newNative(s, cfg) }
		offline = func() (render.Report, error) {
			return render.WAV(score, render.Options{SampleRate: s.rate, Bits: 32, Bars: o.bars, Block: s.block}, io.Discard)
		}
	}
	elapsed := make([]int64, o.blocks*o.runs)
	var left, right [256]float32
	var mallocs, bytes, changes uint64
	var nonzero bool
	for run := 0; run < o.runs; run++ {
		r, err := factory()
		if err != nil {
			return err
		}
		runtime.GC()
		for i := 0; i < o.warmup; i++ {
			r.Render(left[:s.block], right[:s.block])
		}
		previousGC := debug.SetGCPercent(-1)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < o.blocks; i++ {
			start := time.Now()
			r.Render(left[:s.block], right[:s.block])
			elapsed[run*o.blocks+i] = time.Since(start).Nanoseconds()
		}
		runtime.ReadMemStats(&after)
		debug.SetGCPercent(previousGC)
		mallocs += after.Mallocs - before.Mallocs
		bytes += after.TotalAlloc - before.TotalAlloc
		for i := 0; i < s.block; i++ {
			nonzero = nonzero || left[i] != 0 || right[i] != 0
		}
		switch r := r.(type) {
		case *nativeSession:
			if r.fault {
				return fmt.Errorf("native callback faulted")
			}
			changes += r.changes - 1
		case *samplerSession:
			if r.faulted() {
				return fmt.Errorf("standalone sampler faulted")
			}
			changes += r.changes
		}
	}
	if !nonzero {
		return fmt.Errorf("session rendered silence")
	}
	slices.Sort(elapsed)
	p50, p99 := nearestRank(elapsed, 50), nearestRank(elapsed, 99)
	denom := float64(s.block * s.tracks)
	fmt.Fprintf(output, "METRIC CPU path=%s %s voices=%d ratio=%.1f blocks=%d runs=%d warmup_blocks=%d observations=%d p50_ns=%d p99_ns=%d ns_sample_track=%.3f ns_sample_voice=%.3f p99_ns_sample_track=%.3f allocs=%d bytes=%d allocs_block=%.6f scene_changes=%d\n", path, s.key(), s.tracks, samplerRatio(s), o.blocks, o.runs, o.warmup, len(elapsed), p50, p99, float64(p50)/denom, float64(p50)/denom, float64(p99)/denom, mallocs, bytes, float64(mallocs)/float64(len(elapsed)), changes)
	if mallocs != 0 || bytes != 0 {
		return fmt.Errorf("timed callback allocated %d objects (%d bytes)", mallocs, bytes)
	}
	if !o.offline {
		return nil
	}
	if offline == nil {
		fmt.Fprintf(output, "METRIC OFFLINE %s status=unsupported reason=no_sampler_in_render_WAV\n", s.key())
		return nil
	}
	// Full-call timing deliberately includes engine construction, event sorting,
	// encoding, GC and latency compensation; the writer itself is io.Discard.
	offlineTimes := make([]int64, o.runs)
	var report render.Report
	mallocs, bytes = 0, 0
	for i := range offlineTimes {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		var err error
		report, err = offline()
		offlineTimes[i] = time.Since(start).Nanoseconds()
		runtime.ReadMemStats(&after)
		if err != nil {
			return err
		}
		mallocs += after.Mallocs - before.Mallocs
		bytes += after.TotalAlloc - before.TotalAlloc
	}
	slices.Sort(offlineTimes)
	p50, p99 = nearestRank(offlineTimes, 50), nearestRank(offlineTimes, 99)
	blocks := (report.Frames + int64(s.block) - 1) / int64(s.block)
	fmt.Fprintf(output, "METRIC OFFLINE %s scope=full_call bars=%d frames=%d equivalent_blocks=%d runs=%d p50_ns=%d p99_ns=%d ns_block=%.3f ns_sample_track=%.3f allocs_run=%.3f bytes_run=%.3f\n", s.key(), o.bars, report.Frames, blocks, o.runs, p50, p99, float64(p50)/float64(blocks), float64(p50)/float64(report.Frames*int64(s.tracks)), float64(mallocs)/float64(o.runs), float64(bytes)/float64(o.runs))
	return nil
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
