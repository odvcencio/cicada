package engine

import (
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func directorEngine(t *testing.T, block int) *Engine {
	t.Helper()
	cfg := Config{SampleRate: 48000, MaxBlock: block, Tracks: 3, MaxVoices: 3, BPMMilli: 120000, Patterns: make([]PatternBank, 3), Scenes: make([]Scene, 2)}
	for i := range 3 {
		cfg.Track[i].Kind = VoiceAcid
		cfg.Patterns[i].Slots[0] = seq.Pattern{Len: 4, GatePercent: 55}
		step, _ := seq.PackStep(seq.Step{Note: 48, Gate: true, Probability: 100, Velocity: 100})
		cfg.Patterns[i].Slots[0].Steps[0] = step
	}
	cfg.Scenes[0].Track[0] = SceneBinding{Mode: SceneSlot}
	cfg.Scenes[0].Track[1] = SceneBinding{Mode: SceneOff}
	cfg.Scenes[1].Track[0] = SceneBinding{Mode: SceneOff}
	cfg.Scenes[1].Track[1] = SceneBinding{Mode: SceneSlot}
	for i := range cfg.Scenes {
		cfg.Scenes[i].Track[2] = SceneBinding{Mode: SceneOff}
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func renderDirector(t *testing.T, e *Engine, frames int) []cmd.Message {
	t.Helper()
	l, r := make([]float32, e.maxBlock), make([]float32, e.maxBlock)
	var messages []cmd.Message
	for frames > 0 {
		n := min(frames, len(l))
		e.Render(l[:n], r[:n])
		frames -= n
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault: %+v", m)
			}
			messages = append(messages, m)
		}
	}
	return messages
}

func TestDirectorQuantizedStingerAndCrossfade(t *testing.T) {
	for _, q := range []uint32{1, 2, 21} {
		t.Run(string(rune('a'+q)), func(t *testing.T) {
			e := directorEngine(t, 128)
			for _, c := range []cmd.Command{{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: 2}, {Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpTriggerStinger, Track: 2, Arg0: q, Arg1: 10, Tick: 1}} {
				if !e.Push(c) {
					t.Fatal("push")
				}
			}
			quantum := seq.PPQ
			if q == 2 {
				quantum = seq.TicksPerBar
			}
			if q == 21 {
				quantum = 2 * seq.TicksPerBar
			}
			clock, _ := seq.NewClock(48000, 120000)
			start := int(clock.SampleAtTick(quantum))
			renderDirector(t, e, start)
			if e.patterns[2].active >= 0 {
				t.Fatal("stinger started early")
			}
			messages := renderDirector(t, e, 1)
			if !e.director[2].stinger || e.director[2].gain != 0 {
				t.Fatalf("stinger onset: %+v", e.director[2])
			}
			found := false
			for _, m := range messages {
				if m.Kind == cmd.StingerStarted && m.Tick == quantum {
					found = true
				}
			}
			if !found {
				t.Fatal("missing exact stinger landing")
			}
			end := int(clock.SampleAtTick(quantum + seq.PPQ))
			messages = renderDirector(t, e, end-start)
			if e.patterns[2].active != -1 || e.director[2].stinger || e.director[2].gain != 0 {
				t.Fatal("stinger looped or restored its faded tail")
			}
			found = false
			for _, m := range messages {
				if m.Kind == cmd.StingerEnded && m.Tick == quantum+seq.PPQ {
					found = true
				}
			}
			if !found {
				t.Fatal("missing exact stinger end")
			}
		})
	}
	e := directorEngine(t, 128)
	e.PushBatch([]cmd.Command{{Op: cmd.OpSetState, Track: 255, Index: 0, Arg0: 0}, {Op: cmd.OpPlay, Track: 255}})
	renderDirector(t, e, 1)
	e.Push(cmd.Command{Op: cmd.OpSetState, Track: 255, Index: 1, Arg0: 1, Arg1: 100})
	renderDirector(t, e, 1)
	if e.patterns[0].active < 0 || e.patterns[1].active < 0 || e.director[0].gain != 1 || e.director[1].gain != 0 {
		t.Fatal("crossfade does not overlap tracks")
	}
	renderDirector(t, e, 50)
	if math.Abs(float64(e.director[0].gain-.5)) > 1e-6 || math.Abs(float64(e.director[1].gain-.5)) > 1e-6 {
		t.Fatalf("midpoint: %v %v", e.director[0].gain, e.director[1].gain)
	}
	renderDirector(t, e, 50)
	if e.patterns[0].active != -1 || e.patterns[1].active < 0 || e.director[1].gain != 1 {
		t.Fatal("crossfade endpoint")
	}
}

func TestDirectorRenderBlockInvariantAndAllocationFree(t *testing.T) {
	run := func(block int) ([]float32, []cmd.Message) {
		e := directorEngine(t, block)
		e.PushBatch([]cmd.Command{{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: 2}, {Op: cmd.OpSetState, Track: 255}, {Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpTriggerStinger, Track: 2, Arg0: 1, Arg1: 37, Tick: 100}, {Op: cmd.OpSetState, Track: 255, Index: 1, Arg0: 1 | 2<<16, Arg1: 300, Tick: 200}})
		var pcm []float32
		var events []cmd.Message
		l, r := make([]float32, block), make([]float32, block)
		for remaining := 100000; remaining > 0; {
			n := min(remaining, block)
			e.Render(l[:n], r[:n])
			pcm = append(pcm, l[:n]...)
			remaining -= n
			var m cmd.Message
			for e.Poll(&m) {
				switch m.Kind {
				case cmd.Fault:
					t.Fatalf("fault: %+v", m)
				case cmd.StingerStarted, cmd.StingerEnded, cmd.StateChanged:
					events = append(events, m)
				}
			}
		}
		return pcm, events
	}
	a, ma := run(1)
	b, mb := run(128)
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(ma, mb) {
		t.Fatal("director depends on render block size")
	}
	e := directorEngine(t, 128)
	l, r := make([]float32, 128), make([]float32, 128)
	allocs := testing.AllocsPerRun(10, func() {
		e.Reset()
		e.PushBatch([]cmd.Command{{Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpTriggerStinger, Track: 2}, {Op: cmd.OpSetState, Track: 255, Arg0: 1, Arg1: 48}})
		for range 4 {
			e.Render(l, r)
			var m cmd.Message
			for e.Poll(&m) {
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("render allocations: %g", allocs)
	}
}

func TestDirectorStopCancelsScheduledEvents(t *testing.T) {
	e := directorEngine(t, 128)
	e.PushBatch([]cmd.Command{{Op: cmd.OpPlay, Track: 255}, {Op: cmd.OpTriggerStinger, Track: 2, Tick: seq.PPQ}, {Op: cmd.OpSetState, Track: 255, Arg0: 1, Tick: seq.TicksPerBar}, {Op: cmd.OpStop, Track: 255}})
	renderDirector(t, e, 128)
	for _, c := range e.pending[:e.pendingLen] {
		if c.Op == cmd.OpSetState || c.Op == cmd.OpTriggerStinger {
			t.Fatal("stop kept director command")
		}
	}
}

func TestDirectorInitialLayerObservation(t *testing.T) {
	e := directorEngine(t, 128)
	e.PushBatch([]cmd.Command{{Op: cmd.OpSetLayers, Track: 255, Arg0: 128, Arg1: 3}, {Op: cmd.OpSetLayerMasks, Track: 255, Arg0: 5 | 7<<16, Arg1: 7 | 7<<16}, {Op: cmd.OpPlay, Track: 255}})
	messages := renderDirector(t, e, 1)
	for _, m := range messages {
		if m.Kind == cmd.LayerChanged && m.A == 0 && m.B == 5 && m.Tick == 0 {
			return
		}
	}
	t.Fatal("first bar did not report the actual layer mask")
}

// A host may send the phrase length and a phrase-quantized request in one render
// block, in either order, or queue both for one tick. The length applies first,
// so the request waits for the next boundary of that length instead of faulting
// or using an earlier length.
func TestDirectorPhraseLengthPrecedesRequestsInTheSameBlock(t *testing.T) {
	phrase := uint32(cmd.QuantizePhrase)
	state := cmd.Command{Op: cmd.OpSetState, Track: 255, Index: 1, Arg0: 1 | phrase<<16}
	stinger := cmd.Command{Op: cmd.OpTriggerStinger, Track: 2, Arg0: phrase, Arg1: 10}
	clock, _ := seq.NewClock(48000, 120000)
	const now = 3 * seq.TicksPerBar / 2 // the middle of a two-bar phrase
	for _, tc := range []struct {
		name    string
		earlier uint32 // a phrase length set in an earlier block, or zero
		bars    uint32
		request cmd.Command
		first   bool  // the request arrives ahead of the length
		tick    int64 // queue both for this tick instead of sending them at now
		kind    cmd.Kind
		landing int64
	}{
		{"state after length", 0, 2, state, false, 0, cmd.StateChanged, 2 * seq.TicksPerBar},
		{"state before length", 0, 2, state, true, 0, cmd.StateChanged, 2 * seq.TicksPerBar},
		{"stinger after length", 0, 2, stinger, false, 0, cmd.StingerStarted, 2 * seq.TicksPerBar},
		{"both queued for one tick", 0, 2, state, false, now, cmd.StateChanged, 2 * seq.TicksPerBar},
		{"new length replaces an earlier one", 2, 4, state, false, 0, cmd.StateChanged, 4 * seq.TicksPerBar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := directorEngine(t, 128)
			push := func(commands ...cmd.Command) {
				t.Helper()
				if !e.PushBatch(commands) {
					t.Fatal("push")
				}
			}
			if tc.earlier != 0 {
				push(cmd.Command{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: tc.earlier})
			}
			push(cmd.Command{Op: cmd.OpPlay, Track: 255})
			batch := []cmd.Command{{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: tc.bars}, tc.request}
			if tc.first {
				batch[0], batch[1] = batch[1], batch[0]
			}
			if tc.tick != 0 {
				batch[0].Tick, batch[1].Tick = tc.tick, tc.tick
				push(batch...)
			}
			renderDirector(t, e, int(clock.SampleAtTick(now)))
			if tc.tick == 0 {
				push(batch...)
			}
			var landed []cmd.Message
			for _, m := range renderDirector(t, e, int(clock.SampleAtTick(tc.landing)-clock.SampleAtTick(now))+1) {
				if m.Kind == tc.kind {
					landed = append(landed, m)
				}
			}
			if len(landed) != 1 || landed[0].Tick != tc.landing {
				t.Fatalf("landing %+v, want one at tick %d", landed, tc.landing)
			}
		})
	}
}

// The length counts only when it applies no later than the request. One stamped
// for a later tick has not applied when the request is due, so the request still
// faults, as the manual says.
func TestDirectorPhraseLengthForALaterTickDoesNotCount(t *testing.T) {
	e := directorEngine(t, 128)
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("push")
	}
	clock, _ := seq.NewClock(48000, 120000)
	const now = 3 * seq.TicksPerBar / 2 // the middle of a two-bar phrase
	renderDirector(t, e, int(clock.SampleAtTick(now)))
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: 2, Tick: now + seq.PPQ},
		{Op: cmd.OpSetState, Track: 255, Index: 1, Arg0: 1 | uint32(cmd.QuantizePhrase)<<16},
	}) {
		t.Fatal("push")
	}
	l, r := make([]float32, 128), make([]float32, 128)
	e.Render(l, r)
	var m cmd.Message
	for e.Poll(&m) {
		if m.Kind == cmd.Fault && m.A == cmd.FaultDirectorQuantize {
			return
		}
	}
	t.Fatal("a phrase length stamped for a later tick counted as set")
}

// Only the phrase length is read by a state request; the other live setup
// commands fill tables that the bar line reads. So the whole setup and the first
// state may share a block in any order, and the messages and the state in force
// equal those of a host that renders a block between setup and state.
func TestDirectorLiveSetupAndFirstStateShareABlock(t *testing.T) {
	all := []cmd.Command{
		{Op: cmd.OpDefineMacro, Track: 255, Arg0: math.Float32bits(.9)},
		{Op: cmd.OpSetLayers, Track: 255, Arg0: 128, Arg1: 3},
		{Op: cmd.OpSetLayerMasks, Track: 255, Arg0: 5 | 7<<16, Arg1: 7 | 7<<16},
		{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: 2},
		{Op: cmd.OpSetState, Track: 255, Index: 1, Arg0: 1 | uint32(cmd.QuantizePhrase)<<16},
	}
	type result struct {
		messages []cmd.Message
		mask     trackMask
		scene    int
		macro    float32
	}
	clock, _ := seq.NewClock(48000, 120000)
	run := func(batches ...[]cmd.Command) result {
		e := directorEngine(t, 128)
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		frames := int(clock.SampleAtTick(3 * seq.TicksPerBar / 2))
		messages := renderDirector(t, e, frames)
		for _, batch := range batches {
			if !e.PushBatch(batch) {
				t.Fatal("push")
			}
			messages = append(messages, renderDirector(t, e, 128)...)
			frames += 128
		}
		messages = append(messages, renderDirector(t, e, int(clock.SampleAtTick(2*seq.TicksPerBar))+256-frames)...)
		return result{messages, e.layerMask, e.currentScene, e.macroCurrent[0]}
	}
	want := run(all[:4], all[4:])
	landing := int64(-1)
	for _, m := range want.messages {
		if m.Kind == cmd.StateChanged {
			landing = m.Tick
		}
	}
	if landing != 2*seq.TicksPerBar || want.mask != 7 || want.scene != 1 || want.macro != .9 {
		t.Fatalf("separate blocks: landing %d, %+v", landing, want)
	}
	for _, order := range [][]int{{0, 1, 2, 3, 4}, {4, 0, 1, 2, 3}, {3, 4, 0, 1, 2}, {0, 1, 2, 4, 3}, {4, 3, 2, 1, 0}} {
		batch := make([]cmd.Command, len(order))
		for i, k := range order {
			batch[i] = all[k]
		}
		if got := run(batch); !reflect.DeepEqual(got, want) {
			t.Fatalf("order %v differs from setup and state in separate blocks", order)
		}
	}
}
