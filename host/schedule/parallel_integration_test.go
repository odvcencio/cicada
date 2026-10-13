//go:build !tinygo && !js

package schedule_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func parallelSongConfig(t *testing.T, tracks int) engine.Config {
	t.Helper()
	var source strings.Builder
	source.WriteString("cicada 1\ntempo 120\nseed 7419\nfx drive {shape=soft gain=9db tone=9khz mix=0.35}\nfx delay {time=1/8T feedback=0.23 damp=6khz pingpong=true width=1 mix=1}\nfx reverb {size=0.7 decay=1.2s damp=8khz highpass=120hz mix=1}\n")
	for i := 0; i < tracks; i++ {
		kind := "acid"
		if i%16 == 15 {
			kind = "drums"
		}
		fmt.Fprintf(&source, "track t%d %s {level=-36db insert=drive send_a=0.1 send_b=0.08}\n", i, kind)
	}
	source.WriteString("pattern a acid steps=4 swing=58% {1 5 . 3}\npattern b acid steps=4 {3 . 5 1}\npattern beat drums steps=4 {bd: x...;sd: ..x.;ch: xxxx;}\nscene verse {\n")
	for i := 0; i < tracks; i++ {
		pattern := "a"
		if i%16 == 15 {
			pattern = "beat"
		}
		fmt.Fprintf(&source, "t%d=%s\n", i, pattern)
	}
	source.WriteString("}\nscene chorus {\n")
	for i := 0; i < tracks; i++ {
		pattern := "b"
		if i%16 == 15 {
			pattern = "beat"
		}
		fmt.Fprintf(&source, "t%d=%s\n", i, pattern)
	}
	source.WriteString("}\nsong {verse chorus}\n")
	score, ds := notation.Parse([]byte(source.String()))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("songfixture: %v %v", ds, more)
	}
	cfg, err := project.CompileEngine(p, 48000, 256)
	if err != nil {
		t.Fatal(err)
	}
	cfg.LoopSong = true
	cfg.Scenes[1].Settings = []engine.SceneSetting{{Track: 0, ID: kernel.ParamAcidCutoff, Value: 1800}, {Track: uint8(tracks - 1), ID: kernel.ParamMixPan, Value: .45}}
	cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAcidCutoff), Arg0: math.Float32bits(1100), Tick: 173}, {Op: cmd.OpSetParam, Track: uint8(tracks - 1), Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(-.35), Tick: 2119}, {Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-31), Tick: 4871}}
	return cfg
}

func compareParallelRender(t *testing.T, cfg engine.Config, frames int, streamed bool) {
	t.Helper()
	serial, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer serial.Close()
	// Match the engine's own parallel tests: a loaded machine can starve a
	// lane for longer than the 100 ms production floor, which retires the pool
	// and silences the output. This changes the test, not production behavior.
	cfg.ParallelWaitFloor = time.Second
	parallel, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer parallel.Close()
	if err := parallel.EnableParallel(0); err != nil {
		t.Fatal(err)
	}
	play := cmd.Command{Op: cmd.OpPlay, Track: 255}
	serial.Push(play)
	parallel.Push(play)
	var sl, sr, pl, pr [256]float32
	blocks := []int{64, 128, 251, 127, 32}
	tempoChanged, sought := false, false
	for at, block := 0, 0; at < frames; block++ {
		// External controls deliberately fall on buffer boundaries while source
		// pattern/automation/scene events occur inside those varying buffers.
		if at >= 30000 && !tempoChanged {
			tempoChanged = true
			control := cmd.Command{Op: cmd.OpSetTempo, Track: 255, Arg0: 145000}
			serial.Push(control)
			parallel.Push(control)
		}
		if at >= 100000 && !sought {
			sought = true
			control := cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: 0, Arg1: 240}
			serial.Push(control)
			parallel.Push(control)
		}
		n := min(blocks[block%len(blocks)], frames-at)
		if streamed {
			n = min(prepare(t, serial, n), prepare(t, parallel, n))
		}
		serial.Render(sl[:n], sr[:n])
		parallel.Render(pl[:n], pr[:n])
		for i := 0; i < n; i++ {
			if math.Float32bits(sl[i]) != math.Float32bits(pl[i]) || math.Float32bits(sr[i]) != math.Float32bits(pr[i]) {
				t.Fatalf("tracks%d frame%d serial(%g,%g) parallel(%g,%g)", cfg.Tracks, at+i, sl[i], sr[i], pl[i], pr[i])
			}
		}
		drain(t, serial)
		drain(t, parallel)
		at += n
	}
	if streamed && (serial.StreamStats().MissingFrames != 0 || parallel.StreamStats().MissingFrames != 0) {
		t.Fatalf("streamedmisses serial%+v parallel%+v", serial.StreamStats(), parallel.StreamStats())
	}
}
func TestParallelFullRenderKeepsPatternAutomationSceneTempoSeekAndLoopPCM(t *testing.T) {
	for _, tracks := range []int{32, 64} {
		t.Run(fmt.Sprint(tracks), func(t *testing.T) { compareParallelRender(t, parallelSongConfig(t, tracks), 310000, false) })
	}
}
func TestParallelFullRenderKeepsStreamedClipEndAndEffectPCM(t *testing.T) {
	root, p, _ := streamingFixture(t, 300000, 48000, 1)
	p.Clips[0].EndFrame = 2097
	p.Clips[0].FadeOutFrames = 37
	track, placement := p.Tracks[0], p.Arrange.Placements[0]
	p.Tracks = make([]project.Track, 64)
	p.Arrange.Placements = make([]project.Placement, 64)
	for i := 0; i < 64; i++ {
		p.Tracks[i] = track
		p.Tracks[i].ID = fmt.Sprintf("vox%d", i)
		p.Tracks[i].Mixer.GainDB = -42
		p.Arrange.Placements[i] = placement
		p.Arrange.Placements[i].ID = fmt.Sprintf("p%d", i)
		p.Arrange.Placements[i].Track = p.Tracks[i].ID
	}
	cfg, err := schedule.Compile(p, root, 48000, 256)
	if err != nil {
		t.Fatal(err)
	}
	drive, comp, deess := fx.DefaultDriveParams(), fx.DefaultCompParams(), fx.DefaultDeesserParams()
	for i := 0; i < cfg.Tracks; i++ {
		cfg.Track[i].InsertChain = []engine.TrackEffectConfig{{Kind: engine.TrackEffectDrive, Drive: &drive}, {Kind: engine.TrackEffectComp, Comp: &comp}, {Kind: engine.TrackEffectDeesser, Deesser: &deess}}
	}
	compareParallelRender(t, cfg, 8000, true)
}
