package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func testConfig() Config {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 2, MaxVoices: 12, BPMMilli: 120_000, Seed: 4242}
	cfg.Track[0].Kind = VoiceAcid
	cfg.Track[1].Kind = VoiceDrums
	return cfg
}

func TestLiveAcidAndDrumsRenderDeterministically(t *testing.T) {
	first, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	commands := []cmd.Command{
		{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8},
		{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 110<<8 | 1<<16},
	}
	for _, c := range commands {
		if !first.Push(c) || !second.Push(c) {
			t.Fatal("command rejected")
		}
	}
	var aL, aR, bL, bR [128]float32
	energy := float64(0)
	for block := 0; block < 20; block++ {
		first.Render(aL[:], aR[:])
		second.Render(bL[:], bR[:])
		for i := range aL {
			if aL[i] != bL[i] || aR[i] != bR[i] {
				t.Fatalf("render differs at block %d frame %d", block, i)
			}
			energy += float64(aL[i]*aL[i] + aR[i]*aR[i])
		}
	}
	if energy < 1e-7 {
		t.Fatal("live engine was silent")
	}
}

func TestFutureTickCommandIsSampleScheduled(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks = 1
	cfg.MaxVoices = 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) || !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8, Tick: seq.PPQ}) {
		t.Fatal("command rejected")
	}
	var left, right [128]float32
	for block := 0; block < 150; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			if sample != 0 {
				t.Fatal("future note sounded before its tick")
			}
		}
	}
	energy := float32(0)
	for block := 0; block < 60; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			energy += sample * sample
		}
	}
	if energy == 0 {
		t.Fatal("future note never sounded")
	}
}

func TestUnsupportedCommandFaultsAndResetRecovers(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks = 1
	cfg.MaxVoices = 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 0, Arg0: 0}) {
		t.Fatal("valid wire command rejected")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	var message cmd.Message
	if !e.Poll(&message) || message.Kind != cmd.Fault {
		t.Fatalf("missing unsupported-command fault: %+v", message)
	}
	e.Reset()
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("reset engine rejected note")
	}
	energy := float32(0)
	for block := 0; block < 4; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			energy += sample * sample
		}
	}
	if energy == 0 {
		t.Fatal("reset engine remained silent")
	}
}

func TestRenderDoesNotAllocate(t *testing.T) {
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("note rejected")
	}
	var left, right [128]float32
	allocs := testing.AllocsPerRun(1000, func() { e.Render(left[:], right[:]) })
	if allocs != 0 {
		t.Fatalf("Engine.Render allocated %.2f objects", allocs)
	}
}

func BenchmarkRender128(b *testing.B) {
	e, err := New(testConfig())
	if err != nil {
		b.Fatal(err)
	}
	e.meterRate = 0
	if !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) || !e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 110<<8}) {
		b.Fatal("note rejected")
	}
	var left, right [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Render(left[:], right[:])
	}
}

func BenchmarkPatternRender128(b *testing.B) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	e.meterRate = 0
	step, err := seq.PackStep(seq.Step{Note: 45, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil || !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 64},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: step},
		{Op: cmd.OpSelectPattern, Track: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		b.Fatal("pattern setup rejected")
	}
	var left, right [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Render(left[:], right[:])
	}
}

func BenchmarkPatternRender16Tracks128(b *testing.B) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 16, MaxVoices: 16, BPMMilli: 120_000}
	for track := range cfg.Track {
		cfg.Track[track].Kind = VoiceAcid
	}
	e, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	e.meterRate = 0
	step, err := seq.PackStep(seq.Step{Note: 45, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil {
		b.Fatal(err)
	}
	var commands [49]cmd.Command
	for track := 0; track < 16; track++ {
		commands[3*track] = cmd.Command{Op: cmd.OpSetPatternLen, Track: uint8(track), Index: 64}
		commands[3*track+1] = cmd.Command{Op: cmd.OpSetStep, Track: uint8(track), Index: 0, Arg0: step}
		commands[3*track+2] = cmd.Command{Op: cmd.OpSelectPattern, Track: uint8(track)}
	}
	commands[48] = cmd.Command{Op: cmd.OpPlay, Track: 0xff}
	if !e.PushBatch(commands[:]) {
		b.Fatal("pattern setup rejected")
	}
	var left, right [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Render(left[:], right[:])
	}
}

func BenchmarkLiveRender16Tracks128(b *testing.B) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 16, MaxVoices: 16, BPMMilli: 120_000}
	var commands [16]cmd.Command
	for track := range cfg.Track {
		cfg.Track[track].Kind = VoiceAcid
		commands[track] = cmd.Command{Op: cmd.OpNoteOn, Track: uint8(track), Arg0: 45 | 100<<8}
	}
	e, err := New(cfg)
	if err != nil || !e.PushBatch(commands[:]) {
		b.Fatal("live voice setup rejected")
	}
	e.meterRate = 0
	var left, right [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.Render(left[:], right[:])
	}
}

func packedNote(t *testing.T, note uint8) uint32 {
	t.Helper()
	packed, err := seq.PackStep(seq.Step{Note: note, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil {
		t.Fatal(err)
	}
	return packed
}

func TestPatternPlaybackGatesAndQuantizedSwitch(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	commands := []cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 2, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45), Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 1, Arg0: packedNote(t, 48), Arg1: 0},
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 52), Arg1: 1},
		{Op: cmd.OpSelectPattern, Track: 0, Index: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}
	if !e.PushBatch(commands) {
		t.Fatal("project command batch rejected")
	}
	var left, right [128]float32
	var notes, offs, switches []cmd.Message
	for block := 0; block < 225; block++ {
		if block == 20 {
			if !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg0: 1}) {
				t.Fatal("quantized switch rejected")
			}
		}
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			switch message.Kind {
			case cmd.NoteOn:
				notes = append(notes, message)
			case cmd.NoteOff:
				offs = append(offs, message)
			case cmd.Switched:
				switches = append(switches, message)
			case cmd.Fault:
				t.Fatalf("engine fault %d", message.A)
			}
		}
	}
	if len(notes) < 5 || notes[0].Tick != 0 || notes[0].A != 45 || notes[1].Tick != 240 || notes[1].A != 48 {
		t.Fatalf("unexpected first pattern notes: %+v", notes)
	}
	if len(offs) < 2 || offs[0].Tick != 132 || offs[1].Tick != 372 {
		t.Fatalf("unexpected gate releases: %+v", offs)
	}
	if len(switches) != 2 || switches[1].Tick != 960 || switches[1].A != 1 {
		t.Fatalf("unexpected switches: %+v", switches)
	}
	for _, note := range notes {
		if note.A == 52 && note.Tick < 960 {
			t.Fatalf("new pattern sounded before quantized boundary: %+v", note)
		}
	}
	if notes[len(notes)-1].A != 52 {
		t.Fatalf("new pattern did not sound: %+v", notes)
	}
}

func TestPatternRenderIsBlockInvariantAndAllocationFree(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.meterRate, b.meterRate = 0, 0
	commands := []cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45)},
		{Op: cmd.OpSelectPattern, Track: 0, Index: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}
	if !a.PushBatch(commands) || !b.PushBatch(commands) {
		t.Fatal("commands rejected")
	}
	var aL, aR, bL, bR [128]float32
	for block := 0; block < 240; block++ {
		a.Render(aL[:], aR[:])
		b.Render(bL[:64], bR[:64])
		b.Render(bL[64:], bR[64:])
		if aL != bL || aR != bR {
			t.Fatalf("pattern audio changed with block size at block %d", block)
		}
		var am, bm cmd.Message
		for {
			aok, bok := a.Poll(&am), b.Poll(&bm)
			if aok != bok || aok && am != bm {
				t.Fatalf("pattern messages changed with block size at block %d: %+v / %+v", block, am, bm)
			}
			if !aok {
				break
			}
		}
	}
	allocs := testing.AllocsPerRun(100, func() { a.Render(aL[:], aR[:]) })
	if allocs != 0 {
		t.Fatalf("pattern Render allocated %.2f objects", allocs)
	}
}

func TestCriticalMessagesSurviveQueuePressure(t *testing.T) {
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for i := range e.messages {
		kind := cmd.Switched
		if i == 0 {
			kind = cmd.Meter
		}
		e.emit(cmd.Message{Kind: kind, A: uint16(i)})
	}
	e.emit(cmd.Message{Kind: cmd.Switched, A: 999})
	if e.faulted {
		t.Fatal("meter was not coalesced to retain switch")
	}
	count, meters, last := 0, 0, cmd.Message{}
	var message cmd.Message
	for e.Poll(&message) {
		count++
		if message.Kind == cmd.Meter {
			meters++
		}
		last = message
	}
	if count != len(e.messages) || meters != 0 || last.Kind != cmd.Switched || last.A != 999 {
		t.Fatalf("message coalescing lost a critical event: count=%d meters=%d last=%+v", count, meters, last)
	}
	for i := range e.messages {
		e.emit(cmd.Message{Kind: cmd.Switched, A: uint16(i)})
	}
	e.emit(cmd.Message{Kind: cmd.Switched, A: 1000})
	if !e.faulted {
		t.Fatal("critical message overflow did not fault")
	}
	count = 0
	for e.Poll(&message) {
		count++
		if count == 257 && (message.Kind != cmd.Switched || message.A != 1000) {
			t.Fatalf("overflow switch was lost: %+v", message)
		}
		if count == 258 && (message.Kind != cmd.Fault || message.A != 11) {
			t.Fatalf("overflow fault was lost: %+v", message)
		}
	}
	if count != 258 {
		t.Fatalf("critical queue returned %d messages, want 258", count)
	}
}

func TestInvalidRenderBlockFaults(t *testing.T) {
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.Render(nil, nil)
	var message cmd.Message
	if !e.Poll(&message) || message.Kind != cmd.Fault {
		t.Fatalf("empty block did not fault: %+v", message)
	}
}

func TestMaskedLayerContinuesVoiceRelease(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpPlay, Track: 0xff},
		{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8},
	}) {
		t.Fatal("note setup rejected")
	}
	var left, right [128]float32
	for block := 0; block < 20; block++ {
		e.Render(left[:], right[:])
	}
	if !e.voices[0].acid.Active() {
		t.Fatal("acid note was not active before mask")
	}
	tick := e.transport.Tick()
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpNoteOff, Track: 0, Tick: tick},
		{Op: cmd.OpSetLayerMask, Track: 0xff, Arg0: 0, Tick: tick},
	}) {
		t.Fatal("mask and note-off rejected")
	}
	for block := 0; block < 80; block++ {
		e.Render(left[:], right[:])
		for _, sample := range left {
			if block > 1 && sample != 0 {
				t.Fatal("masked layer reached the output after limiter lookahead")
			}
		}
	}
	if e.voices[0].acid.Active() {
		t.Fatal("masked acid envelope stopped advancing")
	}
	if !e.Push(cmd.Command{Op: cmd.OpSetLayerMask, Track: 0xff, Arg0: 1, Tick: e.transport.Tick()}) {
		t.Fatal("unmask rejected")
	}
	e.Render(left[:], right[:])
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatalf("layer transition fault %d", message.A)
		}
	}
}

func TestMaskedDrumLayerKeepsSynthesisTime(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 11
	cfg.Track[0].Kind = VoiceDrums
	masked, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []*Engine{masked, reference} {
		engine.meterRate = 0
		if !engine.PushBatch([]cmd.Command{
			{Op: cmd.OpPlay, Track: 0xff},
			{Op: cmd.OpNoteOn, Track: 0, Index: 0, Arg0: 36 | 110<<8},
		}) {
			t.Fatal("drum setup rejected")
		}
	}
	var left, right [128]float32
	for block := 0; block < 20; block++ {
		masked.Render(left[:], right[:])
		reference.Render(left[:], right[:])
	}
	if !masked.Push(cmd.Command{Op: cmd.OpSetLayerMask, Track: 0xff, Arg0: 0, Tick: masked.transport.Tick()}) {
		t.Fatal("drum mask rejected")
	}
	for block := 0; block < 60; block++ {
		masked.Render(left[:], right[:])
		reference.Render(left[:], right[:])
	}
	leftMasked, rightMasked := masked.voices[0].drums.NextStereo()
	leftReference, rightReference := reference.voices[0].drums.NextStereo()
	if leftMasked != leftReference || rightMasked != rightReference {
		t.Fatalf("masked drum synthesis time diverged: (%g, %g), want (%g, %g)", leftMasked, rightMasked, leftReference, rightReference)
	}
}

func TestLayerMaskAppliesAtNextBar(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.transport.SeekTick(seq.TicksPerBar - 10); err != nil {
		t.Fatal(err)
	}
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpPlay, Track: 0xff},
		{Op: cmd.OpSetLayerMask, Track: 0xff, Arg0: 0},
	}) {
		t.Fatal("layer mask setup rejected")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	if e.layerMask != 1 {
		t.Fatalf("mask applied before the bar boundary at tick %d", e.transport.Tick())
	}
	e.Render(left[:], right[:])
	if e.layerMask != 0 {
		t.Fatalf("mask missed the bar boundary at tick %d", e.transport.Tick())
	}
}

func TestRestartAndNextBarPatternEdit(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 3},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45)},
		{Op: cmd.OpSetStep, Track: 0, Index: 1, Arg0: packedNote(t, 48)},
		{Op: cmd.OpSetStep, Track: 0, Index: 2, Arg0: packedNote(t, 50)},
		{Op: cmd.OpSelectPattern, Track: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		t.Fatal("pattern setup rejected")
	}
	var left, right [128]float32
	var atBeat, beforeEdit, afterEdit uint16
	for block := 0; block < 800; block++ {
		if block == 20 {
			if !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0, Arg0: 1, Arg1: 1}) || !e.Push(cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 52)}) {
				t.Fatal("restart or edit rejected")
			}
		}
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("pattern command fault %d", message.A)
			}
			if message.Kind != cmd.NoteOn {
				continue
			}
			if message.Tick == seq.PPQ {
				atBeat = message.A
			}
			if message.Tick == seq.TicksPerBar-seq.TicksPerStep {
				beforeEdit = message.A
			}
			if message.Tick == seq.TicksPerBar {
				afterEdit = message.A
			}
		}
	}
	if atBeat != 45 || beforeEdit != 50 || afterEdit != 52 {
		t.Fatalf("restart/edit notes: beat=%d before=%d after=%d", atBeat, beforeEdit, afterEdit)
	}
}
