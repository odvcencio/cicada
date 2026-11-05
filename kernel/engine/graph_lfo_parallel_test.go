//go:build !tinygo && !js

package engine

import (
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
)

func TestGraphLFOParallelRequestRendersDeterministically(t *testing.T) {
	cfg := sourceChainConfig(t)
	cfg.Tracks, cfg.MaxVoices, cfg.ParallelWaitFloor = 8, 8, 30*time.Second
	cfg.Track[0].Kind = VoiceGraph
	cfg.Track[0].Graph = graph.Program{Len: 4, Output: 3}
	cfg.Track[0].Graph.Nodes[0] = graph.Node{Op: graph.Pitch}
	cfg.Track[0].Graph.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	cfg.Track[0].Graph.Nodes[2] = graph.Node{Op: graph.LFO, Value: 2}
	cfg.Track[0].Graph.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2}
	for i := 1; i < cfg.Tracks; i++ {
		cfg.Track[i] = cfg.Track[0]
		cfg.Patterns = append(cfg.Patterns, cfg.Patterns[0])
	}
	serial, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer serial.Close()
	requested, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer requested.Close()
	if err := requested.EnableParallel(4); err != nil {
		t.Fatal(err)
	}
	if requested.parallel != nil || requested.parallelAvailable() || requested.ParallelWorkers() != 1 {
		t.Fatal("graph LFOs must use and report the serial frame clock")
	}
	serial.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	requested.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var sl, sr, pl, pr [128]float32
	nonzero := false
	for block := 0; block < 200; block++ {
		serial.Render(sl[:], sr[:])
		requested.Render(pl[:], pr[:])
		for i := range sl {
			if sl[i] != pl[i] || sr[i] != pr[i] {
				t.Fatalf("block %d sample %d differs", block, i)
			}
			nonzero = nonzero || sl[i] != 0
		}
	}
	if !nonzero {
		t.Fatal("render was silent")
	}
}
