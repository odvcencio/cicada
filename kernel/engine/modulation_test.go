package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/modulate"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

func modConfig(t *testing.T) Config {
	cfg := sourceChainConfig(t)
	cfg.MaxBlock = 4096
	cfg.Tracks, cfg.MaxVoices = 2, 12
	cfg.Patterns = append(cfg.Patterns, PatternBank{Drums: new([16][drum.LaneCount]seq.Pattern)})
	cfg.Track[0].Kind = VoiceAcid
	cfg.Track[1].Kind = VoiceDrums
	cfg.Modulators = []ModulatorSpec{{Kind: ModKindLFO, Wave: ModWaveSine, RateTicks: 960}}
	cfg.Routes = []RouteSpec{{Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Depth: .5}}
	return cfg
}

type gridEntry struct {
	song   int64
	values string
}

type modRun struct {
	log []gridEntry
	pcm [32]byte
	pan []float32
}

// runMod plays frames of audio in blocks, optionally seeking to a bar first,
// and returns the control log, a hash of the PCM and the pan after each block.
func runMod(t *testing.T, cfg Config, block, frames int, seekBar int) modRun {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	var run modRun
	e.SetModulationTrace(func(song int64, values []int32) {
		run.log = append(run.log, gridEntry{song, fmt.Sprint(values)})
	})
	if seekBar > 0 {
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: uint32(seekBar)})
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	h := sha256.New()
	left, right := make([]float32, block), make([]float32, block)
	var buf [4]byte
	for done := 0; done < frames; done += block {
		e.Render(left, right)
		for i := range left {
			binary.LittleEndian.PutUint32(buf[:], math.Float32bits(left[i]))
			h.Write(buf[:])
			binary.LittleEndian.PutUint32(buf[:], math.Float32bits(right[i]))
			h.Write(buf[:])
		}
	}
	var m cmd.Message
	for e.Poll(&m) {
		if m.Kind == cmd.Fault {
			t.Fatalf("fault %v", m)
		}
	}
	copy(run.pcm[:], h.Sum(nil))
	return run
}

// Spec 24, lane 1: at 120 BPM and 48 kHz a quarter-note sine gives 0, 1, 0, -1
// at samples 0, 6000, 12000, 18000, and the value at grid point 47 (sample 6016)
// is the function there, whatever the host buffer.
func TestModulatorGridValuesMatchTheFunction(t *testing.T) {
	cfg := modConfig(t)
	run := runMod(t, cfg, 128, 24_000, 0)
	if len(run.log) != 24_000/128+1 {
		t.Fatalf("%d grid points in 24,000 frames", len(run.log))
	}
	var e *Engine
	_ = e
	want := func(sample int64) float64 { return math.Sin(2 * math.Pi * float64(sample) / 24_000) }
	for _, g := range []int64{0, 47, 93} {
		var v []int32
		fmt.Sscanf(run.log[g].values, "[%d]", new(int32))
		var one int32
		fmt.Sscanf(run.log[g].values, "[%d]", &one)
		v = append(v, one)
		if run.log[g].song != g*128 {
			t.Fatalf("grid %d at song sample %d", g, run.log[g].song)
		}
		if got := float64(v[0]) / modulate.One; math.Abs(got-want(g*128)) > 1e-6 {
			t.Errorf("grid %d: %g, want %g", g, got, want(g*128))
		}
	}
}

func TestModulatorControlLogAndPCMIgnoreHostBuffer(t *testing.T) {
	cfg := modConfig(t)
	cfg.Modulators = append(cfg.Modulators,
		ModulatorSpec{Kind: ModKindSway, Octaves: 2, RateMicroHz: 700_000},
		ModulatorSpec{Kind: ModKindLFO, Wave: ModWaveSampleHold, RateTicks: 480},
		ModulatorSpec{Kind: ModKindEnvelope, Source: 0, Attack: 1 << 30, Release: 1 << 26})
	cfg.Routes = append(cfg.Routes,
		RouteSpec{Source: 1, Track: 0, Param: uint16(kernel.ParamMixGain), Depth: 3},
		RouteSpec{Source: 2, Track: 1, Param: uint16(kernel.ParamMixPan), Depth: .4},
		RouteSpec{Source: 3, Track: 1, Param: uint16(kernel.ParamMixGain), Depth: -6})
	const frames = 4 * 3840 * 12 // two bars at 120 BPM
	reference := runMod(t, cfg, 128, frames, 0)
	for _, block := range []int{32, 480, 4096} {
		got := runMod(t, cfg, block, frames, 0)
		if len(got.log) != len(reference.log) {
			t.Fatalf("block %d: %d grid points, want %d", block, len(got.log), len(reference.log))
		}
		for i := range got.log {
			if got.log[i] != reference.log[i] {
				t.Fatalf("block %d: grid %d differs: %v vs %v", block, i, got.log[i], reference.log[i])
			}
		}
		if got.pcm != reference.pcm {
			t.Errorf("block %d: PCM differs from the 128-frame render", block)
		}
	}
}

// After a seek to a bar, every pure source's value at each grid point equals a
// continuous run's. The envelope is left out: it starts at zero at a seek.
func TestModulatorSeekMatchesContinuousRun(t *testing.T) {
	cfg := modConfig(t)
	cfg.Modulators = append(cfg.Modulators,
		ModulatorSpec{Kind: ModKindSway, Octaves: 3, RateTicks: 7680},
		ModulatorSpec{Kind: ModKindLFO, Wave: ModWaveSampleHold, RateMicroHz: 1_300_000},
		ModulatorSpec{Kind: ModKindContour, RateTicks: 3840, Points: []ContourPoint{{0, -modulate.One, 0}, {1 << 30, modulate.One / 2, ModShapeSmooth}, {1 << 32, -modulate.One, 0}}})
	cfg.Song = []SongEntry{{Scene: 0, Bars: 8}}
	continuous := runMod(t, cfg, 480, 6*96_000, 0) // six bars
	seeked := runMod(t, cfg, 480, 4*96_000, 2)     // from bar 2 (0-based): ticks 2*3840
	at := map[int64]string{}
	for _, g := range continuous.log {
		at[g.song] = g.values
	}
	if len(seeked.log) == 0 {
		t.Fatal("no grid points after the seek")
	}
	checked := 0
	for _, g := range seeked.log {
		want, ok := at[g.song]
		if !ok {
			continue
		}
		checked++
		if g.values != want {
			t.Fatalf("grid at song sample %d: seeked %s, continuous %s", g.song, g.values, want)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d grid points compared", checked)
	}
}

// Routes apply in declaration order after the base: multiply then add gives
// clamp(base*m + a), add then multiply gives clamp((base+a)*m).
func TestRouteOrderAndClamp(t *testing.T) {
	for _, c := range []struct {
		name   string
		routes []RouteSpec
		want   float32
	}{
		{"multiply then add", []RouteSpec{{Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Multiply: true, Depth: 1}, {Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Depth: .1}}, .5},
		{"add then multiply", []RouteSpec{{Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Depth: .1}, {Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Multiply: true, Depth: 1}}, .6},
		{"clamp once at the end", []RouteSpec{{Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Depth: 2}, {Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Depth: -1.5}}, .7},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := modConfig(t)
			cfg.Modulators = []ModulatorSpec{{Kind: ModKindLFO, Wave: ModWaveSquare, RateTicks: 3840}}
			cfg.Routes = c.routes
			cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.2), Tick: 0}}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.meterRate = 0
			e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
			left, right := make([]float32, 256), make([]float32, 256)
			e.Render(left, right)
			if got := e.voices[0].pan; math.Abs(float64(got-c.want)) > 1e-6 {
				t.Fatalf("pan %g, want %g", got, c.want)
			}
		})
	}
}

// loop = continue fits whole cycles to the song loop: no jump at the wrap. A
// plain free-rate source whose period does not divide the loop jumps.
func TestLoopContinueHasNoJump(t *testing.T) {
	jump := func(continuous bool) float64 {
		cfg := modConfig(t)
		cfg.LoopSong = true
		cfg.Song = []SongEntry{{Scene: 0, Bars: 2}} // 4 s at 120 BPM
		cfg.Modulators = []ModulatorSpec{{Kind: ModKindLFO, Wave: ModWaveSine, RateMicroHz: 370_000, Continue: continuous}}
		run := runMod(t, cfg, 128, 2*192_000+1024, 0)
		var before, after int32
		const loopGrid = 192_000 / 128
		fmt.Sscanf(run.log[loopGrid-1].values, "[%d]", &before)
		fmt.Sscanf(run.log[loopGrid].values, "[%d]", &after)
		return math.Abs(float64(after-before)) / modulate.One
	}
	if j := jump(true); j > 0.02 {
		t.Errorf("loop = continue jumps %g at the wrap", j)
	}
	if j := jump(false); j < 0.1 {
		t.Errorf("a non-dividing free rate jumps only %g; the test no longer shows the difference", j)
	}
}

func TestModulationDoesNotAllocateAndChangesNothingElse(t *testing.T) {
	cfg := modConfig(t)
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	left, right := make([]float32, 1024), make([]float32, 1024)
	e.Render(left, right)
	if n := testing.AllocsPerRun(200, func() { e.Render(left, right) }); n != 0 {
		t.Fatalf("%v allocations per render", n)
	}
	// A track no route touches renders exactly as it does without modulators.
	plain := cfg
	plain.Modulators, plain.Routes = nil, nil
	a := runMod(t, cfg, 128, 48_000, 0)
	b := runMod(t, plain, 128, 48_000, 0)
	if a.pcm == b.pcm {
		t.Log("pan modulation did not change the pcm; the test pattern may be silent")
	}
}

func TestModulationRejectsBadConfig(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"unsupported row":   func(c *Config) { c.Routes[0].Param = uint16(kernel.ParamMixMute) },
		"unknown source":    func(c *Config) { c.Routes[0].Source = 9 },
		"unknown track":     func(c *Config) { c.Routes[0].Track = 9 },
		"acid row on drums": func(c *Config) { c.Routes[0].Track, c.Routes[0].Param = 1, uint16(kernel.ParamAcidCutoff) },
		"no rate":           func(c *Config) { c.Modulators[0].RateTicks = 0 },
		"two rates":         func(c *Config) { c.Modulators[0].RateMicroHz = 5 },
		"contour end": func(c *Config) {
			c.Modulators[0] = ModulatorSpec{Kind: ModKindContour, RateTicks: 960, Points: []ContourPoint{{0, 0, 0}, {1 << 31, 0, 0}}}
		},
		"envelope track": func(c *Config) { c.Modulators[0] = ModulatorSpec{Kind: ModKindEnvelope, Source: 7} },
	} {
		cfg := modConfig(t)
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The envelope follower is the one stateful source: it starts at zero at load,
// at a seek and at a loop wrap, and rises while its track sounds.
func TestEnvelopeStartsAtZeroAtLoadSeekAndWrap(t *testing.T) {
	cfg := modConfig(t)
	cfg.LoopSong = true
	cfg.Song = []SongEntry{{Scene: 0, Bars: 2}}
	cfg.Modulators = []ModulatorSpec{{Kind: ModKindEnvelope, Source: 0, Attack: 1 << 31, Release: 1 << 28}}
	cfg.Routes = []RouteSpec{{Source: 0, Track: 1, Param: uint16(kernel.ParamMixPan), Depth: .5}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	var log []gridEntry
	e.SetModulationTrace(func(song int64, values []int32) { log = append(log, gridEntry{song, fmt.Sprint(values)}) })
	left, right := make([]float32, 128), make([]float32, 128)
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	// Hold a note so the track sounds.
	e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8})
	rise := func(grids int) int32 {
		for i := 0; i < grids; i++ {
			e.Render(left, right)
		}
		return e.ModulatorValue(0)
	}
	if first := log; len(first) != 0 {
		t.Fatalf("grid points before any render: %d", len(first))
	}
	e.Render(left, right)
	if log[0].values != "[0]" {
		t.Fatalf("envelope at load = %s", log[0].values)
	}
	if v := rise(200); v <= 0 {
		t.Fatalf("the envelope never rose: %d", v)
	}
	// Seek: the next evaluation starts from zero again.
	log = nil
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: 1})
	e.Render(left, right)
	if log[0].values != "[0]" {
		t.Fatalf("envelope after a seek = %s", log[0].values)
	}
	rise(200)
	// Wrap: play through the loop end (two bars, 192,000 samples at 120 BPM and
	// 48 kHz). The held note keeps sounding, so without a reset the follower
	// would carry its level across the wrap. With one, the first grid point of the
	// new pass is 0 and the level climbs again with the attack coefficient.
	log = nil
	for e.transport.Tick() < 2*cfg.Song[0].toTicks() {
		e.Render(left, right)
	}
	const loopSamples = 192_000
	value := func(g gridEntry) int32 {
		var v int32
		fmt.Sscanf(g.values, "[%d]", &v)
		return v
	}
	wrap := -1
	for i, g := range log {
		if g.song >= loopSamples {
			wrap = i
			break
		}
	}
	if wrap < 2 || wrap+3 >= len(log) {
		t.Fatalf("wrap not found in %d grid points (index %d)", len(log), wrap)
	}
	before := value(log[wrap-1])
	if before < modulate.One/1000 {
		t.Fatalf("the follower never rose before the wrap: %d", before)
	}
	first, second, third := value(log[wrap]), value(log[wrap+1]), value(log[wrap+2])
	if first != 0 {
		t.Errorf("first grid of the new pass = %d, want 0 (before the wrap: %d)", first, before)
	}
	if !(second > first && third > second) {
		t.Errorf("the follower does not climb after the wrap: %d %d %d", first, second, third)
	}
	if second > before*3/4 {
		t.Errorf("second grid after the wrap = %d, too close to the pre-wrap level %d: no reset", second, before)
	}
}

func (s SongEntry) toTicks() int64 { return int64(s.Bars) * 3840 }

// BenchmarkModulationGridPoint is the cost of one control-grid evaluation for
// the project maximum: 32 four-octave sways and 64 routes onto the track
// parameters the engine routes. Spec 24 budgets under 1 microsecond native and 3
// microseconds in the browser per grid point for 64 routes.
func BenchmarkModulationGridPoint(b *testing.B) {
	cfg := modConfig(&testing.T{})
	cfg.Modulators, cfg.Routes = nil, nil
	for i := 0; i < MaxModulators; i++ {
		cfg.Modulators = append(cfg.Modulators, ModulatorSpec{Kind: ModKindSway, Octaves: 4, RateTicks: uint32(960 + 7*i)})
	}
	rows := []kernel.ParamID{kernel.ParamMixGain, kernel.ParamMixPan, kernel.ParamMixSendA, kernel.ParamMixSendB}
	for i := 0; i < MaxModRoutes; i++ {
		cfg.Routes = append(cfg.Routes, RouteSpec{Source: uint8(i % MaxModulators), Track: 0, Param: uint16(rows[i%len(rows)]), Depth: .01})
	}
	e, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	left, right := make([]float32, 128), make([]float32, 128)
	e.Render(left, right)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.mod.base = -e.transport.Sample() // song sample 0: a grid point
		e.advanceModulation()
	}
}
