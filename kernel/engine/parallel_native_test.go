//go:build !tinygo && !js

package engine

import (
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/fx"
)

type parallelFixtureFactory struct{ phase uint32 }

func (f parallelFixtureFactory) VoiceCount() int { return 1 }
func (f parallelFixtureFactory) NewStereoVoice(int) (StereoVoice, error) {
	return &parallelFixtureVoice{phase: f.phase}, nil
}

type parallelFixtureVoice struct{ phase uint32 }

func (v *parallelFixtureVoice) NextStereo() (float32, float32) {
	v.phase += 1664525
	x := float32(int32(v.phase)) / 2147483648
	return x * .31, x * -.23
}
func (*parallelFixtureVoice) NoteOn(uint8, uint8) error           { return nil }
func (*parallelFixtureVoice) NoteOff()                            {}
func (v *parallelFixtureVoice) Reset()                            { v.phase = 0 }
func (*parallelFixtureVoice) SelectSlot(uint8, int64, bool) error { return nil }
func (*parallelFixtureVoice) Play() error                         { return nil }

func parallelFixture(t testing.TB, tracks int, drives bool) *Engine {
	t.Helper()
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: tracks, MaxVoices: MaxVoices, BPMMilli: 120000}
	for track := 0; track < tracks; track++ {
		cfg.Track[track].Kind = VoicePrepared
		cfg.Track[track].Prepared = parallelFixtureFactory{phase: uint32(track+1) * 2654435761}
		if drives {
			p := fx.DefaultDriveParams()
			p.GainDB, p.Shape, p.ToneHz = 23, fx.DriveShape(track%4), 2700
			cfg.Track[track].InsertDrive = &p
		}
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestParallelRawTrackPCMandShortSegmentFallback(t *testing.T) {
	for _, tracks := range []int{8, 32, MaxTracks} {
		a, b := parallelFixture(t, tracks, true), parallelFixture(t, tracks, true)
		if err := a.EnableParallel(4); err != nil {
			t.Fatal(err)
		}
		// Race instrumentation changes throughput, not the bounded-join
		// behavior tested separately below.
		if a.parallel != nil {
			a.parallel.waitFloor = time.Second
		}
		t.Cleanup(a.closeParallel)
		for _, segment := range []struct{ start, count int }{{0, 128}, {11, 64}, {0, 1}, {91, 37}, {0, 128}} {
			if frame, fault := a.parallelPrepareSegment(segment.start, segment.count); fault != 0 {
				t.Fatalf("tracks=%d prepare fault=%d frame=%d", tracks, fault, frame)
			}
			for frame := segment.start; frame < segment.start+segment.count; frame++ {
				for track := 0; track < tracks; track++ {
					aL, aR, af := a.renderedTrack(track, frame)
					bL, bR, bf := b.nextRawTrack(track)
					if af != bf || math.Float32bits(aL) != math.Float32bits(bL) || math.Float32bits(aR) != math.Float32bits(bR) {
						t.Fatalf("tracks=%d segment=%+v track=%d frame=%d PCM/fault differs", tracks, segment, track, frame)
					}
				}
			}
			a.parallelEndSegment()
		}
		if allocs := testing.AllocsPerRun(100, func() {
			if _, fault := a.parallelPrepareSegment(0, 128); fault != 0 {
				panic("parallel prepare fault")
			}
			for frame := 0; frame < 128; frame++ {
				for track := 0; track < tracks; track++ {
					a.renderedTrack(track, frame)
				}
			}
			a.parallelEndSegment()
		}); allocs != 0 {
			t.Fatalf("parallel callback allocations=%g", allocs)
		}
	}
}

func TestParallelSingleCoreAndSmallSessionsRemainSerial(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	e := parallelFixture(t, MaxTracks, false)
	if err := e.EnableParallel(4); err != nil {
		t.Fatal(err)
	}
	if e.parallelAvailable() {
		t.Fatal("single core created workers")
	}
	runtime.GOMAXPROCS(old)
	e = parallelFixture(t, 2, false)
	if err := e.EnableParallel(4); err != nil {
		t.Fatal(err)
	}
	if e.parallelAvailable() {
		t.Fatal("small session created workers")
	}
	e = parallelFixture(t, 16, false)
	if err := e.EnableParallel(0); err != nil {
		t.Fatal(err)
	}
	if e.parallelAvailable() {
		t.Fatal("automatic workers enabled below measured session threshold")
	}
	for _, n := range []int{-1, 5} {
		if err := e.EnableParallel(n); err == nil {
			t.Fatalf("invalid lanes %d accepted", n)
		}
	}
}

type parallelStalledVoice struct {
	parallelFixtureVoice
	entered chan struct{}
	release chan struct{}
	once    atomic.Bool
}

func (v *parallelStalledVoice) NextStereo() (float32, float32) {
	if !v.once.Swap(true) {
		close(v.entered)
		<-v.release
	}
	return v.parallelFixtureVoice.NextStereo()
}

func TestParallelTimeoutRetiresAndJoinsBeforeOwnershipReturns(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("worker test requires two scheduler lanes")
	}
	e := parallelFixture(t, 8, false)
	v := &parallelStalledVoice{entered: make(chan struct{}), release: make(chan struct{})}
	e.voices[1].prepared = v
	if err := e.EnableParallel(2); err != nil {
		t.Fatal(err)
	}
	e.parallel.waitFloor = time.Millisecond // Keep this synthetic abandonment test short.
	started := time.Now()
	frame, fault := e.parallelPrepareSegment(0, 32)
	if fault != FaultParallelWorker || frame != 0 {
		close(v.release)
		e.closeParallel()
		t.Fatalf("abandoned worker returned frame=%d fault=%d", frame, fault)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		close(v.release)
		e.closeParallel()
		t.Fatalf("callback wait exceeded bound: %s", elapsed)
	}
	select {
	case <-v.entered:
	default:
		close(v.release)
		e.closeParallel()
		t.Fatal("stall fixture did not own voice")
	}
	if !e.parallelAvailable() {
		t.Fatal("retired pool enabled unsafe serial fallback")
	}
	if _, _, code := e.renderedTrack(1, 0); code != FaultParallelWorker {
		t.Fatal("retired render touched a worker-owned voice")
	}
	closed := make(chan struct{})
	go func() { e.closeParallel(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("join returned while voice was still owned")
	default:
	}
	close(v.release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("worker teardown did not finish after voice release")
	}
	if _, code := e.parallelPrepareSegment(0, 128); code != FaultParallelWorker {
		t.Fatal("retired engine resumed DSP")
	}
	e.closeParallel() // Repeated retirement/close is harmless.
}

type parallelFaultVoice struct {
	parallelFixtureVoice
	frames, faultAt int
	panicInstead    bool
}

func (v *parallelFaultVoice) NextStereo() (float32, float32) {
	if v.frames == v.faultAt {
		if v.panicInstead {
			panic("fixture worker failure")
		}
		return float32(math.NaN()), 0
	}
	v.frames++
	return v.parallelFixtureVoice.NextStereo()
}

func TestParallelPublishesEarliestRawFaultAndWorkerPanic(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("worker test requires two scheduler lanes")
	}
	e := parallelFixture(t, 8, true)
	e.voices[3].prepared = &parallelFaultVoice{faultAt: 9}
	e.voices[5].prepared = &parallelFaultVoice{faultAt: 2}
	if err := e.EnableParallel(4); err != nil {
		t.Fatal(err)
	}
	defer e.closeParallel()
	if frame, fault := e.parallelPrepareSegment(11, 64); frame != 13 || fault != FaultDriveInsert {
		t.Fatalf("earliest raw fault frame=%d code=%d", frame, fault)
	}
	if _, _, fault := e.renderedTrack(5, 12); fault != 0 {
		t.Fatal("valid prefix was discarded")
	}
	if _, _, fault := e.renderedTrack(5, 13); fault != FaultDriveInsert {
		t.Fatal("cached raw fault was lost")
	}
	e.closeParallel()
	e = parallelFixture(t, 8, false)
	e.voices[1].prepared = &parallelFaultVoice{faultAt: 0, panicInstead: true}
	if err := e.EnableParallel(2); err != nil {
		t.Fatal(err)
	}
	defer e.closeParallel()
	if _, fault := e.parallelPrepareSegment(0, 64); fault != FaultParallelWorker {
		t.Fatal("worker panic was not visible")
	}
}

func BenchmarkParallelRawTrackDriveDSP(b *testing.B) {
	for _, tracks := range []int{32, MaxTracks} {
		for _, lanes := range []int{1, 4} {
			name := "serial"
			if lanes > 1 {
				name = "parallel"
			}
			b.Run(name+"/tracks="+strconv.Itoa(tracks), func(b *testing.B) {
				e := parallelFixture(b, tracks, true)
				if err := e.EnableParallel(lanes); err != nil {
					b.Fatal(err)
				}
				defer e.closeParallel()
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, fault := e.parallelPrepareSegment(0, 128); fault != 0 {
						b.Fatal(fault)
					}
					for frame := 0; frame < 128; frame++ {
						for track := 0; track < tracks; track++ {
							e.renderedTrack(track, frame)
						}
					}
					e.parallelEndSegment()
				}
			})
		}
	}
}

// This manual sample reports scheduling tails without imposing a machine-
// dependent performance gate on normal correctness tests. Misses refer to DSP
// processing exceeding one buffer's duration, not measured driver underruns.
func TestParallelCallbackTimingSample(t *testing.T) {
	if os.Getenv("CICADA_PARALLEL_TIMING") != "1" {
		t.Skip("set CICADA_PARALLEL_TIMING=1 to collect callback timing")
	}
	for _, tracks := range []int{16, 32, 64} {
		for _, frames := range []int{64, 128} {
			for _, lanes := range []int{1, 4} {
				e := performanceEngine(t, tracks, frames, lanes)
				l, r := make([]float32, frames), make([]float32, frames)
				runtime.GC()
				for range 50 {
					e.Render(l, r)
				}
				const samples = 600
				times := make([]time.Duration, samples)
				budget := time.Duration(frames) * time.Second / 48000
				var total time.Duration
				misses := 0
				for i := range times {
					started := time.Now()
					e.Render(l, r)
					elapsed := time.Since(started)
					if e.faulted {
						e.Close()
						t.Fatalf("timing sample fault tracks=%d frames=%d lanes=%d", tracks, frames, lanes)
					}
					times[i] = elapsed
					total += elapsed
					if elapsed > budget {
						misses++
					}
				}
				sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
				allocs := testing.AllocsPerRun(50, func() { e.Render(l, r) })
				t.Logf("tracks=%d frames=%d lanes=%d samples=%d mean_us=%.3f p50_us=%.3f p99_us=%.3f max_us=%.3f budget_us=%.3f over_budget=%d allocations=%g", tracks, frames, lanes, samples, float64(total.Microseconds())/samples, float64(times[samples/2].Nanoseconds())/1000, float64(times[samples*99/100].Nanoseconds())/1000, float64(times[samples-1].Nanoseconds())/1000, float64(budget.Nanoseconds())/1000, misses, allocs)
				e.Close()
			}
		}
	}
}
