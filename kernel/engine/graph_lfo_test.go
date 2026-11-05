package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
)

func TestGraphLFOSongClockFollowsTransport(t *testing.T) {
	program := graph.Program{Len: 1, Output: 0}
	program.Nodes[0] = graph.Node{Op: graph.LFO, Value: 1}
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0] = TrackConfig{Kind: VoiceGraph, Graph: program}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	render := func(frames int) {
		t.Helper()
		e.Render(make([]float32, frames), make([]float32, frames))
		if e.faulted {
			t.Fatal("engine fault")
		}
	}
	push := func(command cmd.Command) {
		t.Helper()
		if !e.Push(command) {
			t.Fatal("command rejected")
		}
	}
	push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	render(128)
	if e.graphLFO.clock.Sample != 127 || e.graphLFO.absolute != 128 {
		t.Fatalf("playing clock: %+v", e.graphLFO)
	}
	push(cmd.Command{Op: cmd.OpStop, Track: 255})
	render(128)
	if e.graphLFO.clock.Sample != 128 || e.graphLFO.clock.Playing {
		t.Fatalf("stopped clock: %+v", e.graphLFO)
	}
	push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	render(1)
	if e.graphLFO.clock.Sample != 128 {
		t.Fatalf("resume lost fractional tick: %+v", e.graphLFO)
	}
	push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: 1})
	render(1)
	if want := uint64(e.songSampleAtTick(3840)); e.graphLFO.clock.Sample != want {
		t.Fatalf("seek clock %d, want %d", e.graphLFO.clock.Sample, want)
	}
	e.Reset()
	render(1)
	if e.graphLFO.clock.Sample != 0 || e.graphLFO.clock.Playing {
		t.Fatalf("reset clock: %+v", e.graphLFO)
	}
}

func TestGraphLFOLoopResetsAtActualTempoChangedBoundary(t *testing.T) {
	cfg := sourceChainConfig(t)
	cfg.LoopSong = true
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}}
	cfg.Track[0].Kind = VoiceGraph
	cfg.Track[0].Graph = graph.Program{Len: 1, Output: 0}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.LFO, Value: 1}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.meterRate = 0
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var l, r [1]float32
	for range 1001 {
		e.Render(l[:], r[:])
	}
	// A live tempo change makes the actual played frames disagree with the
	// time computed by applying the new tempo to the whole song.
	e.transport.SetTempoNow(240000)
	for e.transport.Tick() < e.scheduleDuration && e.graphLFO.absolute < 100000 {
		e.Render(l[:], r[:])
	}
	if e.transport.Tick() < e.scheduleDuration {
		t.Fatal("song did not reach its loop")
	}
	e.Render(l[:], r[:])
	if e.graphLFO.clock.Sample != 0 {
		t.Fatalf("LFO did not restart at actual loop boundary: %+v", e.graphLFO)
	}
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: 2, Arg1: 120})
	e.Render(l[:], r[:])
	want := e.songSampleAtTick(2*e.scheduleDuration+120) - e.songSampleAtTick(2*e.scheduleDuration)
	if e.graphLFO.clock.Sample != uint64(want) {
		t.Fatalf("seek within loop: got %d, want %d", e.graphLFO.clock.Sample, want)
	}
}
