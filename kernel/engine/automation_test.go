package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

func TestAutomationSeekLoopAndAllocationFree(t *testing.T) {
	cfg := sourceChainConfig(t)
	cfg.LoopSong = true
	for _, point := range []struct {
		tick  int64
		value float32
	}{{0, -.6}, {4, .2}, {8, .6}, {7680, -.6}} {
		cfg.Automation = append(cfg.Automation, cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(point.value), Tick: point.tick})
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	e.Render(left[:1], right[:1])
	if e.voices[0].pan != -.6 {
		t.Fatalf("initial pan %g", e.voices[0].pan)
	}
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 6})
	e.Render(left[:1], right[:1])
	if e.voices[0].pan != .2 {
		t.Fatalf("seek pan %g", e.voices[0].pan)
	}
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg0: 2, Arg1: 8})
	e.Render(left[:1], right[:1])
	if e.voices[0].pan != .6 {
		t.Fatalf("loop pan %g", e.voices[0].pan)
	}
	if n := testing.AllocsPerRun(1000, func() { e.Render(left[:], right[:]) }); n != 0 {
		t.Fatalf("render allocations %g", n)
	}
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatal(message)
		}
	}
}

func TestAutomationRejectsMalformedTimeline(t *testing.T) {
	for _, controls := range [][]cmd.Command{
		{{Op: cmd.OpPlay, Track: 255}},
		{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Tick: 8}, {Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Tick: 4}},
	} {
		cfg := testConfig()
		cfg.Automation = controls
		if _, err := New(cfg); err == nil {
			t.Fatal("accepted malformed timeline")
		}
	}
}
