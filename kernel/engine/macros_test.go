package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

func macroTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Config{SampleRate: 48_000, MaxBlock: 256, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func pushMacroCommand(t *testing.T, e *Engine, command cmd.Command) {
	t.Helper()
	if !e.Push(command) {
		t.Fatalf("macro command rejected: %+v", command)
	}
}

func defineMacro(t *testing.T, e *Engine, id uint16, value float32) {
	t.Helper()
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpDefineMacro, Track: 255, Index: id, Arg0: math.Float32bits(value)})
}

func setMacro(t *testing.T, e *Engine, id uint16, value float32, frames uint32) {
	t.Helper()
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: id, Arg0: math.Float32bits(value), Arg1: frames})
}

func renderMacroFrames(t *testing.T, e *Engine, frames, blockSize int) {
	t.Helper()
	var left, right [256]float32
	for frames > 0 {
		block := min(frames, blockSize)
		e.Render(left[:block], right[:block])
		frames -= block
	}
}

func TestMacroJumpWithZeroSmoothing(t *testing.T) {
	e := macroTestEngine(t)
	defineMacro(t, e, 0, 0.25)
	setMacro(t, e, 0, 0.75, 0)
	renderMacroFrames(t, e, 1, 1)
	current, target := e.MacroValue(0)
	if current != 0.75 || target != 0.75 {
		t.Fatalf("macro jump: current=%v target=%v, want 0.75", current, target)
	}
	if current, target := e.MacroValue(-1); current != 0 || target != 0 {
		t.Fatalf("negative macro ID returned %v, %v", current, target)
	}
	if current, target := e.MacroValue(16); current != 0 || target != 0 {
		t.Fatalf("out-of-range macro ID returned %v, %v", current, target)
	}
}

func TestMacroSmoothingReachesTargetExactly(t *testing.T) {
	e := macroTestEngine(t)
	defineMacro(t, e, 0, 0)
	setMacro(t, e, 0, 1, 4800)
	renderMacroFrames(t, e, 4799, 256)
	if current, target := e.MacroValue(0); current == target {
		t.Fatalf("ramp reached target early: current=%v target=%v", current, target)
	}
	renderMacroFrames(t, e, 1, 1)
	if current, target := e.MacroValue(0); current != 1 || target != 1 {
		t.Fatalf("ramp did not reach target exactly: current=%v target=%v", current, target)
	}
}

func TestMacroSmoothingIsBlockSizeIndependent(t *testing.T) {
	newRamp := func(t *testing.T) *Engine {
		t.Helper()
		e := macroTestEngine(t)
		defineMacro(t, e, 0, 0.1)
		setMacro(t, e, 0, 0.9, 4800)
		return e
	}
	large, small := newRamp(t), newRamp(t)
	renderMacroFrames(t, large, 1000, 256)
	renderMacroFrames(t, small, 1000, 64)
	largeAt1000, targetLarge := large.MacroValue(0)
	smallAt1000, targetSmall := small.MacroValue(0)
	if largeAt1000 != smallAt1000 || targetLarge != targetSmall {
		t.Fatalf("values differ at frame 1000: 256=%v/%v 64=%v/%v", largeAt1000, targetLarge, smallAt1000, targetSmall)
	}
	renderMacroFrames(t, large, 3800, 256)
	renderMacroFrames(t, small, 3800, 64)
	largeAt4800, targetLarge := large.MacroValue(0)
	smallAt4800, targetSmall := small.MacroValue(0)
	if largeAt4800 != smallAt4800 || targetLarge != targetSmall {
		t.Fatalf("values differ at frame 4800: 256=%v/%v 64=%v/%v", largeAt4800, targetLarge, smallAt4800, targetSmall)
	}
}

func TestMacroCommandAppliesAtItsTick(t *testing.T) {
	e := macroTestEngine(t)
	defineMacro(t, e, 0, 0.2)
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpPlay, Track: 255})
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: 0, Arg0: math.Float32bits(0.8), Tick: 1})
	renderMacroFrames(t, e, 1, 1)
	if current, target := e.MacroValue(0); current != 0.2 || target != 0.2 {
		t.Fatalf("future command applied before its tick: current=%v target=%v", current, target)
	}
	renderMacroFrames(t, e, 256, 256)
	if current, target := e.MacroValue(0); current != 0.8 || target != 0.8 {
		t.Fatalf("command did not apply at its tick: current=%v target=%v", current, target)
	}
}

func TestMacroRestartFromCurrent(t *testing.T) {
	e := macroTestEngine(t)
	defineMacro(t, e, 0, 0)
	setMacro(t, e, 0, 1, 100)
	renderMacroFrames(t, e, 40, 40)
	before, _ := e.MacroValue(0)
	setMacro(t, e, 0, 0, 100)
	renderMacroFrames(t, e, 1, 1)
	current, target := e.MacroValue(0)
	want := before + (float32(0)-before)/100
	if math.Abs(float64(current-want)) > 1e-6 || target != 0 {
		t.Fatalf("restarted ramp: current=%v target=%v, want current %v target 0 (start %v)", current, target, want, before)
	}
}
