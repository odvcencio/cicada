package engine

import (
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func performedFixture() *seq.Performance {
	return &seq.Performance{LengthTicks: 1920, Notes: []seq.PerformedNote{
		{Tick: 17, EndTick: 700, Note: 60, Velocity: 82},
		{Tick: 20, EndTick: 1300, Note: 64, Velocity: 105},
		{Tick: 480, EndTick: 901, Note: 67, Velocity: 47},
		{Tick: 1500, EndTick: 1700, Note: 60, Velocity: 96},
	}}
}

func performanceConfig(cfg Config, performance *seq.Performance) Config {
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 55, Performance: performance}
	return cfg
}

func TestPerformedPianoPCMMatchesIndependentLiveGates(t *testing.T) {
	for _, block := range []int{1, 127, 1024} {
		cfg := pianoConfig()
		cfg.MaxBlock = block
		performance := performedFixture()
		player, err := New(performanceConfig(cfg, performance))
		if err != nil {
			t.Fatal(err)
		}
		reference, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		player.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
		player.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		reference.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		for iteration := int64(0); iteration < 2; iteration++ {
			for _, note := range performance.Notes {
				reference.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: uint16(note.Note), Arg0: uint32(note.Note) | uint32(note.Velocity)<<8, Tick: iteration*1920 + note.Tick})
				reference.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: uint16(note.Note), Tick: iteration*1920 + note.EndTick})
			}
		}
		clock, _ := seq.NewClock(48000, 120000)
		frames := int(clock.SampleAtTick(3840))
		left, right, wantLeft, wantRight := make([]float32, block), make([]float32, block), make([]float32, block), make([]float32, block)
		for at := 0; at < frames; at += block {
			n := min(block, frames-at)
			player.Render(left[:n], right[:n])
			reference.Render(wantLeft[:n], wantRight[:n])
			for i := range n {
				if left[i] != wantLeft[i] || right[i] != wantRight[i] {
					t.Fatalf("block=%d: performed gates changed PCM at frame %d: %g/%g want %g/%g", block, at+i, left[i], right[i], wantLeft[i], wantRight[i])
				}
			}
			var message cmd.Message
			for player.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatal("performance fault", message)
				}
			}
			for reference.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatal("reference fault", message)
				}
			}
		}
	}
}

func TestPerformedSeekResumeAndSwitchReconstructIndependentGates(t *testing.T) {
	cfg := performanceConfig(pianoConfig(), performedFixture())
	cfg.Patterns[0].Slots[1] = seq.Pattern{Len: 1, GatePercent: 55, Performance: &seq.Performance{LengthTicks: 1920, Notes: []seq.PerformedNote{{Tick: 0, EndTick: 1800, Note: 71, Velocity: 99}}}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 500})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var l, r [128]float32
	render := func(n int) {
		e.Render(l[:n], r[:n])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("playback fault: %+v", m)
			}
		}
	}
	render(1)
	state := e.patterns[0].performed
	for _, pitch := range []uint8{60, 64, 67} {
		if state.gates[pitch].id == 0 {
			t.Fatalf("seek lost held pitch %d", pitch)
		}
	}
	for e.transport.Tick() < 750 {
		render(128)
	}
	if state.gates[60].id != 0 || state.gates[64].id == 0 || state.gates[67].id == 0 {
		t.Fatal("a chased gate released the wrong pitches")
	}
	e.Push(cmd.Command{Op: cmd.OpStop, Track: 255})
	render(1)
	if state.gates != [128]performedGate{} {
		t.Fatal("Stop retained a performed gate")
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	render(1)
	if state.gates[64].id == 0 || state.gates[67].id == 0 {
		t.Fatal("resume lost held notes")
	}
	e.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 0xffff})
	render(1)
	if state.gates != [128]performedGate{} {
		t.Fatal("panic retriggered the gates it just released")
	}
	e.Push(cmd.Command{Op: cmd.OpStop, Track: 255})
	render(1)
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	render(1)
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg1: 1})
	render(1)
	if state.gates[64].id != 0 || state.gates[67].id != 0 || state.gates[71].id == 0 {
		t.Fatal("switch kept old gates or lost the new onset")
	}
	for e.transport.Tick() < 1400 {
		render(128)
	}
	if state.gates[71].id == 0 {
		t.Fatal("old performance releases cut the new pattern")
	}
}

func TestDensePerformanceAdmitsMessagesBeforeRendering(t *testing.T) {
	p := &seq.Performance{LengthTicks: 3840}
	for i := 0; i < seq.MaxPerformanceNotes; i++ {
		p.Notes = append(p.Notes, seq.PerformedNote{Tick: int64(i/128) * 4, EndTick: int64(i/128)*4 + 1, Note: uint8(i % 128), Velocity: 90})
	}
	cfg := performanceConfig(polyConfig(), p)
	cfg.MaxBlock, cfg.BPMMilli = 1024, 300000
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var l, r [1024]float32
	e.Render(l[:], r[:])
	var m cmd.Message
	starts, ends := 0, 0
	for e.Poll(&m) {
		switch m.Kind {
		case cmd.Fault:
			t.Fatalf("dense take faulted: %+v", m)
		case cmd.NoteOn:
			starts++
		case cmd.NoteOff:
			ends++
		}
	}
	if starts != 512 || ends != 512 {
		t.Fatalf("lost transitions: %d starts, %d ends", starts, ends)
	}
}

func TestLongPerformedLoopsFindSharedEndWithoutSearching(t *testing.T) {
	e := &Engine{tracks: 2}
	e.patterns[0].active, e.patterns[1].active = 0, 0
	e.patterns[0].slots[0] = seq.Pattern{Performance: &seq.Performance{LengthTicks: 32}}
	e.patterns[1].slots[0] = seq.Pattern{Performance: &seq.Performance{LengthTicks: 1<<40 - 1}}
	e.patterns[1].startTick = 1
	if tick, ok := e.scenePatternEndTick(); !ok || tick != 1<<40 {
		t.Fatalf("wrong shared performance end: %d %v", tick, ok)
	}
}

func TestPerformedPolyReleasesOnlyMatchingToneAndOwnsItsData(t *testing.T) {
	cfg := performanceConfig(polyConfig(), performedFixture())
	cfg.Track[0].Graph = graph.Program{Len: 4, Output: 3, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}, {Op: graph.Gate}, {Op: graph.Multiply, A: 1, B: 2}}}
	player, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]seq.PerformedNote(nil), cfg.Patterns[0].Slots[0].Performance.Notes...)
	cfg.Patterns[0].Slots[0].Performance.Notes[0].Note = 72
	if !reflect.DeepEqual(player.patterns[0].slots[0].Performance.Notes, want) {
		t.Fatal("performance aliases caller data")
	}
	player.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	player.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	var power float64
	for frame := 0; frame < 30000; frame += 128 {
		player.Render(left[:], right[:])
		if frame > 27000 {
			for _, value := range left {
				power += float64(value) * float64(value)
			}
		}
		var message cmd.Message
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
		}
	}
	if player.patterns[0].performed.gates[64].id == 0 || player.patterns[0].performed.gates[60].id != 0 || power < 1e-8 {
		t.Fatal("releasing the other pitches ended the still-held E4", power)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		player.Render(left[:], right[:])
		var message cmd.Message
		for player.Poll(&message) {
		}
	}); allocations != 0 {
		t.Fatal("performance callback allocated", allocations)
	}
	player.Push(cmd.Command{Op: cmd.OpStop, Track: 255})
	player.Render(left[:], right[:])
	for _, gate := range player.patterns[0].performed.gates {
		if gate.id != 0 {
			t.Fatal("Stop retained a performed gate")
		}
	}
	player.Reset()
	if player.voices[0].poly.ActiveVoices() != 0 {
		t.Fatal("Reset retained a performance voice")
	}
}

func TestPerformedKeyboardAndLiveKeyKeepSeparateGates(t *testing.T) {
	installFakeKeys(t)
	cfg := performanceConfig(keysConfig(), &seq.Performance{LengthTicks: 960, Notes: []seq.PerformedNote{{Tick: 0, EndTick: 240, Note: 60, Velocity: 80}, {Tick: 0, EndTick: 480, Note: 64, Velocity: 100}}})
	player, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	player.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
	player.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var left, right [128]float32
	player.Render(left[:], right[:])
	player.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 60 | 90<<8, Index: 60})
	for frame := 128; frame < 7000; frame += 128 {
		player.Render(left[:], right[:])
	}
	keys := player.voices[0].keys.(*fakeKeys)
	if keys.notes[60] == 0 || keys.notes[64] == 0 {
		t.Fatal("a performance release ended a live or independent chord tone")
	}
	player.Push(cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 60})
	player.Render(left[:], right[:])
	if keys.notes[60] != 0 || keys.notes[64] == 0 {
		t.Fatal("live key release ended another performed tone")
	}
	for _, value := range left {
		if math.IsNaN(float64(value)) {
			t.Fatal("invalid PCM")
		}
	}
}

func TestPerformanceMessageRingKeepsOrderAcrossCounterWrap(t *testing.T) {
	e, err := New(performanceConfig(pianoConfig(), performedFixture()))
	if err != nil {
		t.Fatal(err)
	}
	messages := e.messageStorage()
	e.messageRead, e.messageWrite = ^uint32(0)-128, ^uint32(0)-128
	for i := range messages {
		e.emit(cmd.Message{Kind: cmd.NoteOn, A: uint16(i)})
	}
	var m cmd.Message
	for i := range messages {
		if !e.Poll(&m) || m.A != uint16(i) {
			t.Fatalf("message order changed at wrap: %d %+v", i, m)
		}
	}
	if e.Poll(&m) || e.faulted {
		t.Fatal("wrapped message ring faulted or duplicated a record")
	}
}
