//go:build !tinygo

package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
)

// liveConfig is one mono graph track whose envelope time is node 3, bound to
// slot 0 (instrument.p1).
func liveConfig() Config {
	cfg := Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, Patterns: []PatternBank{{}}}
	cfg.Track[0] = TrackConfig{Kind: VoiceGraph, Graph: graph.Program{Len: 6, Output: 5, Nodes: [graph.MaxNodes]graph.Node{
		{Op: graph.Pitch}, {Op: graph.Sine, A: 0}, {Op: graph.Gate}, {Op: graph.Constant, Value: 100}, {Op: graph.Envelope, A: 2, B: 3}, {Op: graph.Multiply, A: 1, B: 4}}}}
	cfg.LiveParams = []LiveParam{{Track: 0, Slot: 0, Node: 3}}
	return cfg
}

func setSlot(slot int, value float32) cmd.Command {
	return cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamInstrumentP1 + kernel.ParamID(slot)), Arg0: math.Float32bits(value)}
}

func TestLiveParamGlidesToItsTargetAndSnapsWhenImmediate(t *testing.T) {
	e, err := New(liveConfig())
	if err != nil {
		t.Fatal(err)
	}
	p := &e.voices[0].live[0]
	if !p.bound || p.cur != 100 || p.target != 100 {
		t.Fatalf("binding took %+v, want the authored 100", *p)
	}
	e.apply(setSlot(0, 300))
	if e.faulted || p.target != 300 || p.cur != 100 || !e.voices[0].liveMoving {
		t.Fatalf("a smoothed set changed the value at once or faulted: %+v faulted=%v", *p, e.faulted)
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	if p.cur <= 100 || p.cur >= 300 {
		t.Fatalf("after one block the value is %g, want it between 100 and 300", p.cur)
	}
	for i := 0; i < 200 && e.voices[0].liveMoving; i++ {
		e.Render(left[:], right[:])
	}
	if p.cur != 300 || e.voices[0].liveMoving {
		t.Fatalf("the glide did not settle on 300: %+v", *p)
	}
	e.setParamMode(setSlot(0, 50), true)
	if p.cur != 50 || p.target != 50 {
		t.Fatalf("an immediate set did not snap: %+v", *p)
	}
}

func TestLiveParamUnboundSlotFaults(t *testing.T) {
	e, err := New(liveConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.apply(setSlot(1, 2))
	if !e.faulted {
		t.Fatal("a set to an unbound slot was accepted")
	}
}

func TestLiveParamBindingMustNameAConstantNode(t *testing.T) {
	for _, binding := range []LiveParam{{Slot: 0, Node: 4}, {Slot: 8, Node: 3}, {Slot: 0, Node: 40}, {Track: 1, Slot: 0, Node: 3}} {
		cfg := liveConfig()
		cfg.LiveParams = []LiveParam{binding}
		if _, err := New(cfg); err == nil {
			t.Fatalf("binding %+v was accepted", binding)
		}
	}
	cfg := liveConfig()
	cfg.LiveParams = []LiveParam{{Slot: 0, Node: 3}, {Slot: 0, Node: 3}}
	if _, err := New(cfg); err == nil {
		t.Fatal("a slot bound twice was accepted")
	}
}

// A scene restore returns a moved parameter to its authored value.
func TestLiveParamRestoresItsAuthoredValue(t *testing.T) {
	e, err := New(liveConfig())
	if err != nil {
		t.Fatal(err)
	}
	var saved sceneTrackParameters
	saved.liveValues = e.voices[0].liveTargets()
	e.setParamMode(setSlot(0, 400), true)
	if !e.restoreVoiceState(&e.voices[0], &saved) {
		t.Fatal("restore failed")
	}
	if p := e.voices[0].live[0]; p.cur != 100 || p.target != 100 || e.voices[0].liveChanged != 0 {
		t.Fatalf("restore left %+v", p)
	}
}
