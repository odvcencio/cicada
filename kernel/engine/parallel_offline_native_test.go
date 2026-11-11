//go:build !tinygo && !js

package engine

import (
	"math"
	"runtime"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
)

// offlineGraphConfig is tracks graph voices, each playing the source-chain
// patterns at its own pitch, with drive inserts on odd tracks and the delay
// and reverb sends set, so segments split at many note events and the mix
// reads every track.
func offlineGraphConfig(t *testing.T, tracks int) Config {
	cfg := sourceChainConfig(t)
	cfg.Tracks, cfg.MaxVoices = tracks, tracks
	cfg.Track[0].Kind = VoiceGraph
	cfg.Track[0].Graph = graph.Program{Len: 3, Output: 2}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.Pitch}
	cfg.Track[0].Graph.Nodes[1] = graph.Node{Op: graph.Saw, A: 0}
	cfg.Track[0].Graph.Nodes[2] = graph.Node{Op: graph.Multiply, A: 1, B: 1}
	for i := 1; i < tracks; i++ {
		cfg.Track[i] = cfg.Track[0]
		bank := cfg.Patterns[0]
		for slot := range bank.Slots[:3] {
			p := bank.Slots[slot]
			p.Transpose = int8(i % 12)
			bank.Slots[slot] = p
		}
		cfg.Patterns = append(cfg.Patterns, bank)
	}
	for i := 1; i < tracks; i += 2 {
		p := fx.DefaultDriveParams()
		p.GainDB, p.Shape = 9, fx.DriveShape(i%4)
		cfg.Track[i].InsertDrive = &p
	}
	return cfg
}

// renderOffline renders blocks of frames from a fresh engine with lanes
// offline lanes (1 is the serial render) and returns the output and taps.
func renderOffline(t *testing.T, cfg Config, lanes, frames, blocks int) ([]float32, []TapFrame, int) {
	t.Helper()
	cfg.MaxBlock = frames
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.EnableOfflineParallel(lanes); err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	out := make([]float32, 0, 2*frames*blocks)
	taps := make([]TapFrame, 0, frames*blocks)
	left, right, blockTaps := make([]float32, frames), make([]float32, frames), make([]TapFrame, frames)
	var m cmd.Message
	for range blocks {
		e.RenderWithTaps(left, right, blockTaps)
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("lanes %d: fault %d", lanes, m.A)
			}
		}
		for i := range left {
			out = append(out, left[i], right[i])
		}
		taps = append(taps, blockTaps...)
	}
	if stats := e.OfflineParallelStats(); lanes > 1 && runtime.GOMAXPROCS(0) > 1 && stats.ParallelSegments == 0 {
		t.Fatalf("lanes %d: no segment ran on more than one lane (%+v)", lanes, stats)
	}
	return out, taps, e.ParallelWorkers()
}

func TestOfflineParallelRenderIsBitIdentical(t *testing.T) {
	// These voices are too light for lanes to pay; run them on every lane.
	defer renderlanes.ForceEveryLane()()
	for _, tracks := range []int{2, 5, 12} {
		cfg := offlineGraphConfig(t, tracks)
		for _, frames := range []int{128, 4096} {
			blocks := 96000 / frames
			serial, serialTaps, serialLanes := renderOffline(t, cfg, 1, frames, blocks)
			if serialLanes != 1 {
				t.Fatalf("lanes 1 set up %d lanes", serialLanes)
			}
			nonzero := false
			for _, v := range serial {
				nonzero = nonzero || v != 0
			}
			if !nonzero {
				t.Fatal("serial render is silent")
			}
			for _, lanes := range []int{2, 3, 4, 8} {
				got, taps, used := renderOffline(t, cfg, lanes, frames, blocks)
				if want := min(lanes, tracks); used != want {
					t.Fatalf("tracks %d lanes %d: set up %d lanes, want %d", tracks, lanes, used, want)
				}
				for i := range serial {
					if math.Float32bits(serial[i]) != math.Float32bits(got[i]) {
						t.Fatalf("tracks %d block %d lanes %d: sample %d differs", tracks, frames, lanes, i)
					}
				}
				for i := range serialTaps {
					if serialTaps[i] != taps[i] {
						t.Fatalf("tracks %d block %d lanes %d: taps of frame %d differ", tracks, frames, lanes, i)
					}
				}
			}
		}
	}
}

func TestOfflineParallelSetup(t *testing.T) {
	cfg := offlineGraphConfig(t, 6)
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.EnableOfflineParallel(0); err == nil {
		t.Fatal("zero lanes accepted")
	}
	if err := e.EnableOfflineParallel(1); err != nil || e.parallel != nil || e.ParallelWorkers() != 1 {
		t.Fatalf("one lane must stay serial: err %v", err)
	}
	if err := e.EnableOfflineParallel(64); err != nil || e.ParallelWorkers() != 6 || e.ParallelPriority() != "offline" {
		t.Fatalf("lanes %d priority %q err %v", e.ParallelWorkers(), e.ParallelPriority(), err)
	}
	if err := e.EnableOfflineParallel(2); err == nil {
		t.Fatal("a second configuration was accepted")
	}
	if err := e.EnableParallel(4); err == nil {
		t.Fatal("live lanes accepted on top of offline lanes")
	}
	// Live playback keeps its own thresholds: six tracks stay serial.
	live, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.EnableParallel(0); err != nil || live.parallel != nil {
		t.Fatal("automatic live lanes started below 32 tracks")
	}
	// Graph LFOs share the serial frame clock, offline as live.
	lfo := cfg
	lfo.Track[0].Graph.Nodes[1] = graph.Node{Op: graph.LFO, Value: 2}
	withLFO, err := New(lfo)
	if err != nil {
		t.Fatal(err)
	}
	defer withLFO.Close()
	if err := withLFO.EnableOfflineParallel(4); err != nil || withLFO.parallel != nil {
		t.Fatal("graph LFOs must keep the serial render")
	}
}

// A raw fault inside a lane reaches the output at the frame where the serial
// render meets it: same samples before it, same fault code, silence after.
func TestOfflineParallelFaultMatchesSerial(t *testing.T) {
	render := func(lanes int) ([]float32, uint16) {
		e := parallelFixture(t, 12, true)
		defer e.Close()
		e.voices[7].prepared = &parallelFaultVoice{faultAt: 3000}
		if err := e.EnableOfflineParallel(lanes); err != nil {
			t.Fatal(err)
		}
		var out []float32
		var fault uint16
		left, right := make([]float32, 128), make([]float32, 128)
		var m cmd.Message
		for range 40 {
			e.Render(left, right)
			out = append(out, left...)
			out = append(out, right...)
			for e.Poll(&m) {
				if m.Kind == cmd.Fault && fault == 0 {
					fault = m.A
				}
			}
		}
		return out, fault
	}
	defer renderlanes.ForceEveryLane()()
	serial, serialFault := render(1)
	if serialFault == 0 {
		t.Fatal("the fixture did not fault")
	}
	for _, lanes := range []int{2, 4} {
		got, fault := render(lanes)
		if fault != serialFault {
			t.Fatalf("lanes %d: fault %d, serial %d", lanes, fault, serialFault)
		}
		for i := range serial {
			if math.Float32bits(serial[i]) != math.Float32bits(got[i]) {
				t.Fatalf("lanes %d: sample %d differs", lanes, i)
			}
		}
	}
}

type offlinePanicVoice struct{ parallelFixtureVoice }

func (v *offlinePanicVoice) NextStereo() (float32, float32) { panic("voice failed") }

// A panic in a lane is raised on the render goroutine, as the serial render
// raises it, after every lane has stopped.
func TestOfflineParallelPanicReachesRender(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two processors")
	}
	e := parallelFixture(t, 8, false)
	defer e.Close()
	e.voices[5].prepared = &offlinePanicVoice{}
	if err := e.EnableOfflineParallel(4); err != nil {
		t.Fatal(err)
	}
	defer renderlanes.ForceEveryLane()()
	defer func() {
		if value := recover(); value != "voice failed" {
			t.Fatalf("recovered %v", value)
		}
	}()
	left, right := make([]float32, 128), make([]float32, 128)
	e.Render(left, right)
	t.Fatal("Render returned")
}
