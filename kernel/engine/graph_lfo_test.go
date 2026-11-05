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
