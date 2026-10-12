package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
)

// sceneTempoConfig plays bar 0 at 120 BPM, bar 1 at 60 BPM (set by scene 1),
// and bar 2 at the carried 60 BPM. One acid note starts every bar.
func sceneTempoConfig() Config {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 4
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
	for step := 0; step < 16; step += 4 {
		cfg.Patterns[0].Slots[0].Steps[step] = packedNoteValue(45 + uint8(step))
	}
	cfg.Scenes = []Scene{
		{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot, Slot: 0}}},
		{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot, Slot: 0}}, Settings: []SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 60}}},
		{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot, Slot: 0}}},
	}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
	return cfg
}

func packedNoteValue(note uint8) uint32 {
	v, err := seq.PackStep(seq.Step{Note: note, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil {
		panic(err)
	}
	return v
}

type tempoRun struct {
	left, right []float32
	ticks       []int64
	bpm         []int64
}

func renderSceneTempo(t *testing.T, cfg Config, block int, frames int, setup func(*Engine)) (*Engine, tempoRun) {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if setup != nil {
		setup(e)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	var run tempoRun
	l, r := make([]float32, block), make([]float32, block)
	var message cmd.Message
	for done := 0; done < frames; {
		n := min(block, frames-done)
		e.Render(l[:n], r[:n])
		run.left, run.right = append(run.left, l[:n]...), append(run.right, r[:n]...)
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("fault %d", message.A)
			}
			if message.Kind == cmd.NoteOn {
				run.ticks = append(run.ticks, message.Tick)
			}
		}
		done += n
	}
	return e, run
}

func TestSceneTempoStepsAtBarIndependentOfBlockSize(t *testing.T) {
	cfg := sceneTempoConfig()
	frames := 96_000 + 2*192_000 + 100
	e64, run64 := renderSceneTempo(t, cfg, 64, frames, nil)
	_, run128 := renderSceneTempo(t, cfg, 128, frames, nil)
	if len(run64.left) != len(run128.left) {
		t.Fatal("length differs")
	}
	for i := range run64.left {
		if run64.left[i] != run128.left[i] || run64.right[i] != run128.right[i] {
			t.Fatalf("audio depends on block size at frame %d", i)
		}
	}
	if len(run64.ticks) != len(run128.ticks) {
		t.Fatalf("note counts differ %d %d", len(run64.ticks), len(run128.ticks))
	}
	for i := range run64.ticks {
		if run64.ticks[i] != run128.ticks[i] {
			t.Fatalf("note %d tick differs", i)
		}
	}
	// 120 BPM: one bar is 96000 samples; 60 BPM: 192000.
	if e64.transport.BPMMilli() != 60_000 {
		t.Fatalf("tempo after the slow scenes = %d", e64.transport.BPMMilli())
	}
	if got := e64.songSampleAtTick(seq.TicksPerBar); got != 96_000 {
		t.Fatalf("bar 1 starts at sample %d, want 96000", got)
	}
	if got := e64.songSampleAtTick(2 * seq.TicksPerBar); got != 96_000+192_000 {
		t.Fatalf("bar 2 starts at sample %d, want 288000", got)
	}
	// The playing clock re-anchored on exactly that sample.
	anchor := e64.transport.Clock()
	if anchor.AnchorTick != seq.TicksPerBar || anchor.AnchorSample != 96_000 {
		t.Fatalf("clock anchor = tick %d sample %d", anchor.AnchorTick, anchor.AnchorSample)
	}
}

func TestSceneTempoSeekMatchesPlayingThrough(t *testing.T) {
	cfg := sceneTempoConfig()
	seekTo := int64(2*seq.TicksPerBar + 137)
	// Play through to just before the seek position.
	probe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	end := probe.songSampleAtTick(seekTo)
	through, _ := renderSceneTempo(t, cfg, 128, int(end), nil)
	seeking, _ := renderSceneTempo(t, cfg, 128, 0, func(e *Engine) {
		if !e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 2, Arg1: 137}) {
			t.Fatal("seek rejected")
		}
	})
	var l, r [128]float32
	through.Render(l[:], r[:])
	seeking.Render(l[:], r[:])
	if through.transport.BPMMilli() != 60_000 || seeking.transport.BPMMilli() != 60_000 {
		t.Fatalf("tempo through=%d seeking=%d", through.transport.BPMMilli(), seeking.transport.BPMMilli())
	}
	if through.transport.Tick() != seeking.transport.Tick() {
		t.Fatalf("tick through=%d seeking=%d", through.transport.Tick(), seeking.transport.Tick())
	}
	// Seeking back restores the score tempo.
	if !seeking.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff}) {
		t.Fatal("seek back rejected")
	}
	seeking.Render(l[:1], r[:1])
	if seeking.transport.BPMMilli() != 120_000 {
		t.Fatalf("seek to start left tempo %d", seeking.transport.BPMMilli())
	}
	// A seek into the slow scene from a fresh engine and from the end agree.
	if !seeking.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1}) {
		t.Fatal("seek rejected")
	}
	seeking.Render(l[:1], r[:1])
	if seeking.transport.BPMMilli() != 60_000 {
		t.Fatalf("seek into the slow scene gave %d", seeking.transport.BPMMilli())
	}
}

func TestSceneTempoLoopWrapRestoresScoreTempo(t *testing.T) {
	cfg := sceneTempoConfig()
	cfg.LoopSong = true
	probe, _ := New(cfg)
	cycle := int(probe.tempoCycleSamples)
	if cycle != 96_000+2*192_000 {
		t.Fatalf("cycle samples = %d", cycle)
	}
	e, _ := renderSceneTempo(t, cfg, 128, cycle+500, nil)
	if e.transport.BPMMilli() != 120_000 {
		t.Fatalf("looped song tempo = %d, want the score tempo", e.transport.BPMMilli())
	}
	if got := e.transport.Clock().AnchorSample; got != int64(cycle) {
		t.Fatalf("loop re-anchored at %d, want %d", got, cycle)
	}
	if e.songSampleAtTick(e.scheduleDuration+seq.TicksPerBar) != int64(cycle)+96_000 {
		t.Fatal("second cycle sample map is wrong")
	}
}

func TestSceneTempoAndDelayDivisionFadeOnce(t *testing.T) {
	build := func(tempoFirst bool) Config {
		cfg := sceneTempoConfig()
		delay := fx.DefaultDelayParams()
		delay.Mix = .5
		cfg.DelayA = &delay
		cfg.Track[0].SendA = .8
		tempo := SceneSetting{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 60}
		division := SceneSetting{Track: 0xff, ID: kernel.ParamFxDelayTime, Division: fx.Eighth}
		cfg.Scenes[1].Settings = []SceneSetting{tempo, division}
		if !tempoFirst {
			cfg.Scenes[1].Settings = []SceneSetting{division, tempo}
		}
		return cfg
	}
	frames := 96_000 + 192_000
	_, a := renderSceneTempo(t, build(true), 128, frames, nil)
	_, b := renderSceneTempo(t, build(false), 128, frames, nil)
	for i := range a.left {
		if a.left[i] != b.left[i] || a.right[i] != b.right[i] {
			t.Fatalf("setting order changed the delay at frame %d", i)
		}
	}
	var energy float64
	for _, v := range a.left[96_000:] {
		energy += float64(v * v)
	}
	if energy == 0 {
		t.Fatal("silent")
	}
}

func TestSceneTempoRangeAndDelayFit(t *testing.T) {
	for _, bad := range []float32{19, 301, float32(math.NaN())} {
		cfg := sceneTempoConfig()
		cfg.Scenes[1].Settings[0].Value = bad
		if _, err := New(cfg); err == nil {
			t.Fatalf("tempo %v was accepted", bad)
		}
	}
	for _, ok := range []float32{20, 300} {
		cfg := sceneTempoConfig()
		cfg.Scenes[1].Settings[0].Value = ok
		if _, err := New(cfg); err != nil {
			t.Fatalf("tempo %v rejected: %v", ok, err)
		}
	}
	// A half note at 20 BPM is 6 s, which does not fit the 4 s buffer; the
	// score tempo alone would allow it.
	cfg := sceneTempoConfig()
	delay := fx.DefaultDelayParams()
	delay.Division, delay.TimeMs = fx.Half, 0
	cfg.DelayA = &delay
	cfg.Scenes[1].Settings[0].Value = 20
	if _, err := New(cfg); err == nil {
		t.Fatal("a division that does not fit the slowest tempo was accepted")
	}
	// A live parameter command still cannot set the tempo.
	e, _ := New(sceneTempoConfig())
	if e.Push(cmd.Command{Op: cmd.OpSetParam, Track: 0xff, Index: uint16(kernel.ParamGlobalTempo), Arg0: math.Float32bits(90)}) {
		t.Fatal("live tempo command was accepted")
	}
}

func TestSceneTempoClipSeekMatchesPlayingThrough(t *testing.T) {
	cfg := clipScheduleConfig()
	cfg.BPMMilli, cfg.Schedule = 120000, nil
	pcm := make([]float32, 960_000)
	for i := range pcm {
		pcm[i] = float32(i%127) / 1024
	}
	cfg.Assets[0].Left = pcm
	cfg.Clips = []ClipConfig{{EndFrame: int64(len(pcm))}}
	cfg.Scenes = []Scene{
		{Track: [MaxTracks]SceneBinding{{Mode: SceneClip}}},
		{Track: [MaxTracks]SceneBinding{{Mode: SceneKeep}}, Settings: []SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 60}}},
		{},
	}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
	seek := int64(2*seq.TicksPerBar + 137)
	continuous, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	seeking, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	continuous.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var left, right [128]float32
	var message cmd.Message
	for frame, end := int64(0), continuous.songSampleAtTick(seek); frame < end; {
		n := int(min(int64(len(left)), end-frame))
		continuous.Render(left[:n], right[:n])
		for continuous.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
		}
		frame += int64(n)
	}
	if !seeking.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(seek / seq.TicksPerBar), Arg1: uint32(seek % seq.TicksPerBar)}) || !seeking.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("seek rejected")
	}
	var want, got [128]TapFrame
	continuous.RenderWithTaps(left[:], right[:], want[:])
	seeking.RenderWithTaps(left[:], right[:], got[:])
	if !continuous.clipVoices[0].active || continuous.clipVoices[0].sourceFrame != seeking.clipVoices[0].sourceFrame {
		t.Fatalf("clip position through=%g seeking=%g", continuous.clipVoices[0].sourceFrame, seeking.clipVoices[0].sourceFrame)
	}
	for i := range want {
		if want[i].Tracks[0] != got[i].Tracks[0] {
			t.Fatalf("frame %d through=%+v seek=%+v", i, want[i].Tracks[0], got[i].Tracks[0])
		}
	}
	// 96000 samples at 120 BPM plus 192000 at 60 BPM, plus 137 ticks at 60 BPM
	// (50 samples each), plus the 128 frames rendered after the seek.
	if want := int64(96_000 + 192_000 + 137*50 + 128); math.Abs(seeking.clipVoices[0].sourceFrame-float64(want)) > 1 {
		t.Fatalf("clip position %g is not near %d", seeking.clipVoices[0].sourceFrame, want)
	}
}

// FramesBetweenTicks must name the sample where the transport really reaches
// each bar, from any start tick and across a loop wrap.
func TestSceneTempoFramesBetweenTicksMatchesTheTransport(t *testing.T) {
	for _, loop := range []bool{false, true} {
		cfg := sceneTempoConfig()
		cfg.LoopSong = loop
		probe, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		last := int64(3)
		if loop {
			last = 7
		}
		for _, from := range []int64{0, seq.TicksPerBar, 2*seq.TicksPerBar + 137} {
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(from / seq.TicksPerBar), Arg1: uint32(from % seq.TicksPerBar)})
			e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			reached := map[int64]int64{}
			var l, r [1]float32
			for frame := int64(0); frame < probe.FramesBetweenTicks(0, last*seq.TicksPerBar)+10; frame++ {
				e.Render(l[:], r[:])
				// Tick() is the tick after this frame, so the frame that reaches
				// a bar is the one before.
				for bar := int64(1); bar <= last; bar++ {
					if _, seen := reached[bar]; !seen && e.transport.Tick() >= bar*seq.TicksPerBar {
						reached[bar] = frame + 1
					}
				}
			}
			for bar := int64(1); bar <= last; bar++ {
				tick := bar * seq.TicksPerBar
				if tick <= from {
					continue
				}
				if got, want := probe.FramesBetweenTicks(from, tick), reached[bar]; got != want {
					t.Fatalf("loop=%v from=%d bar=%d: export says %d frames, the transport took %d", loop, from, bar, got, want)
				}
			}
		}
	}
}

// Stepping the tempo at a scene boundary, seeking into a slow scene, and
// asking for bar placement all stay off the heap.
func TestSceneTempoRenderSeekAndFramesAllocationFree(t *testing.T) {
	cfg := sceneTempoConfig()
	cfg.LoopSong = true
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	var left, right [128]float32
	allocs := testing.AllocsPerRun(50, func() {
		e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 2})
		for range 3 {
			e.Render(left[:], right[:])
		}
		e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
		e.Render(left[:], right[:])
		_ = e.FramesBetweenTicks(0, 8*seq.TicksPerBar) + e.TempoAtTick(5*seq.TicksPerBar)
	})
	if e.faulted || allocs != 0 {
		t.Fatalf("fault=%v allocations=%g", e.faulted, allocs)
	}
	// A render that crosses the 120 to 60 BPM step and the loop wrap.
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	e.Render(left[:1], right[:1])
	crossing := testing.AllocsPerRun(1, func() {
		for range 6000 {
			e.Render(left[:], right[:])
		}
	})
	if e.faulted || crossing != 0 {
		t.Fatalf("crossing fault=%v allocations=%g", e.faulted, crossing)
	}
}

// A manual launch that lands on the exact song-boundary tick wins the patterns
// but never the tempo: the song owns it, so the engine, the tempo chain and the
// bar-placement export agree.
func TestSceneTempoManualLaunchOnTheBoundaryKeepsTheSongTempo(t *testing.T) {
	pcm := make([]float32, 960_000)
	for i := range pcm {
		pcm[i] = float32(i%127) / 1024
	}
	cfg := clipScheduleConfig()
	cfg.BPMMilli, cfg.Schedule = 120000, nil
	cfg.Assets[0].Left = pcm
	cfg.Clips = []ClipConfig{{EndFrame: int64(len(pcm))}}
	cfg.Scenes = []Scene{
		{Track: [MaxTracks]SceneBinding{{Mode: SceneClip}}},
		{Track: [MaxTracks]SceneBinding{{Mode: SceneKeep}}, Settings: []SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 60}}},
		{Track: [MaxTracks]SceneBinding{{Mode: SceneKeep}}, Settings: []SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 200}}},
		{},
	}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 3, Bars: 1}}
	through, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	seeking, _ := New(cfg)
	through.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var l, r [128]float32
	render := func(e *Engine, frames int64) {
		for frames > 0 {
			n := int(min(128, frames))
			e.Render(l[:n], r[:n])
			frames -= int64(n)
		}
	}
	render(through, 90_000)
	// Launch scene 2 (tempo 200) on the next bar: the tick where scene 1 starts.
	if !through.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 2, Arg0: 2}) {
		t.Fatal("launch rejected")
	}
	render(through, 6_000+256)
	if index, _ := through.CurrentScene(); index != 2 {
		t.Fatalf("the manual scene did not win the boundary: scene %d", index)
	}
	if through.transport.BPMMilli() != 60_000 {
		t.Fatalf("a manual launch changed the tempo to %d", through.transport.BPMMilli())
	}
	// Frames per bar: the transport reaches bar 2 where the export says it does.
	want := through.FramesBetweenTicks(0, 2*seq.TicksPerBar)
	if want != 96_000+192_000 {
		t.Fatalf("export gives %d frames to bar 2", want)
	}
	render(through, want+137*50-96_256)
	if through.transport.Clock().AnchorSample != 96_000 || through.transport.BPMMilli() != 60_000 {
		t.Fatalf("clock anchor %d tempo %d", through.transport.Clock().AnchorSample, through.transport.BPMMilli())
	}
	// 137 ticks into the song's bar 3 (scene 3 keeps the 60 BPM tempo).
	seek := 2*seq.TicksPerBar + 137
	seeking.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 2, Arg1: 137})
	seeking.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	seeking.Render(l[:1], r[:1])
	if through.transport.Tick() < seek || through.transport.Tick() > seek+1 {
		t.Fatalf("through reached tick %d, want %d", through.transport.Tick(), seek)
	}
	if through.transport.BPMMilli() != seeking.transport.BPMMilli() {
		t.Fatalf("tempo through=%d seeking=%d", through.transport.BPMMilli(), seeking.transport.BPMMilli())
	}
	// The clip keeps its recorded speed: elapsed samples through the tempo step.
	if a, b := through.clipVoices[0].sourceFrame, seeking.clipVoices[0].sourceFrame; !through.clipVoices[0].active || math.Abs(a-b) > 150 {
		t.Fatalf("clip position through=%g seeking=%g", a, b)
	}
}
