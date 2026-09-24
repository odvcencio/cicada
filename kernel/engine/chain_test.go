package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func chainCommand(track uint8, position uint16, slot, repeats uint8) cmd.Command {
	return cmd.Command{Op: cmd.OpSetChain, Track: track, Index: position, Arg0: uint32(slot) | uint32(repeats)<<8}
}

func TestPatternChainRepeatsAndLoopsAcrossDifferentLengths(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 2, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45), Arg1: 0},
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 3, Arg1: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 52), Arg1: 1},
		chainCommand(0, 0, 0, 2),
		chainCommand(0, 1, 1, 1),
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		t.Fatal("chain setup rejected")
	}
	var left, right [128]float32
	var notes, switches []cmd.Message
	for block := 0; block < 550; block++ {
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			switch message.Kind {
			case cmd.NoteOn:
				notes = append(notes, message)
			case cmd.Switched:
				switches = append(switches, message)
			case cmd.Fault:
				t.Fatalf("chain fault %d at tick %d", message.A, message.Tick)
			}
		}
	}
	wantNotes := []struct {
		tick int64
		note uint16
	}{{0, 45}, {480, 45}, {960, 52}, {1680, 45}, {2160, 45}, {2640, 52}}
	if len(notes) != len(wantNotes) {
		t.Fatalf("note count %d, want %d: %+v", len(notes), len(wantNotes), notes)
	}
	for i, want := range wantNotes {
		if notes[i].Tick != want.tick || notes[i].A != want.note {
			t.Fatalf("note %d: %+v, want tick %d note %d", i, notes[i], want.tick, want.note)
		}
	}
	wantSwitches := []struct {
		tick int64
		slot uint16
	}{{0, 0}, {960, 1}, {1680, 0}, {2640, 1}}
	if len(switches) != len(wantSwitches) {
		t.Fatalf("switch count %d, want %d: %+v", len(switches), len(wantSwitches), switches)
	}
	for i, want := range wantSwitches {
		if switches[i].Tick != want.tick || switches[i].A != want.slot {
			t.Fatalf("switch %d: %+v, want tick %d slot %d", i, switches[i], want.tick, want.slot)
		}
	}
}

func TestChainEditWaitsForCurrentPatternEnd(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 2, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45), Arg1: 0},
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 2, Arg1: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 52), Arg1: 1},
		{Op: cmd.OpSelectPattern, Track: 0, Index: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		t.Fatal("pattern setup rejected")
	}
	var left, right [128]float32
	var switches []cmd.Message
	for block := 0; block < 110; block++ {
		if block == 10 {
			if !e.PushBatch([]cmd.Command{chainCommand(0, 0, 1, 1), chainCommand(0, 1, 0, 1)}) {
				t.Fatal("live chain edit rejected")
			}
		}
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("chain fault %d", message.A)
			}
			if message.Kind == cmd.Switched {
				switches = append(switches, message)
			}
		}
	}
	if len(switches) < 2 || switches[1].Tick != 2*seq.TicksPerStep || switches[1].A != 1 {
		t.Fatalf("chain edit did not wait for the current pattern end: %+v", switches)
	}
}

func TestChainSwitchCarriesSlideOrReleasesAtNormalGate(t *testing.T) {
	for _, targetPlays := range []bool{true, false} {
		name := "rest"
		if targetPlays {
			name = "carry"
		}
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Tracks, cfg.MaxVoices = 1, 1
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.meterRate = 0
			last, err := seq.PackStep(seq.Step{Note: 45, Gate: true, Slide: true, Ratchet: 1, Probability: 100, Velocity: 100})
			if err != nil {
				t.Fatal(err)
			}
			commands := []cmd.Command{
				{Op: cmd.OpSetStep, Track: 0, Index: 15, Arg0: last, Arg1: 0},
				{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 48), Arg1: 0},
			}
			if targetPlays {
				target, err := seq.PackStep(seq.Step{Note: 52, Gate: true, Accent: true, Ratchet: 1, Probability: 100, Velocity: 100})
				if err != nil {
					t.Fatal(err)
				}
				commands = append(commands, cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: target, Arg1: 1})
			}
			commands = append(commands, chainCommand(0, 0, 0, 1), chainCommand(0, 1, 1, 1), cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			if !e.PushBatch(commands) {
				t.Fatal("chain setup rejected")
			}
			var left, right [128]float32
			for block := 0; block < 750; block++ {
				e.Render(left[:], right[:])
				if block == 740 && (e.patterns[0].playingNote != 0) != targetPlays {
					t.Fatalf("outgoing gate before chain switch=%v, want %v", e.patterns[0].playingNote != 0, targetPlays)
				}
			}
			e.Render(left[:], right[:])
			if e.patterns[0].active != 1 {
				t.Fatal("chain did not switch at the bar boundary")
			}
			if targetPlays && e.voices[0].acid.AccentStrength() != 0 {
				t.Fatal("chain slide retriggered the target accent envelope")
			}
		})
	}
}

func TestChainUsesEditedPatternLengthForNextBoundary(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 2, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45), Arg1: 0},
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 52), Arg1: 1},
		chainCommand(0, 0, 0, 2),
		chainCommand(0, 1, 1, 1),
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 3, Arg1: 0, Tick: seq.TicksPerStep},
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		t.Fatal("chain setup rejected")
	}
	var left, right [128]float32
	var switches []cmd.Message
	for block := 0; block < 300; block++ {
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("chain fault %d", message.A)
			}
			if message.Kind == cmd.Switched {
				switches = append(switches, message)
			}
		}
	}
	if len(switches) != 2 || switches[1].Tick != 6*seq.TicksPerStep || switches[1].A != 1 {
		t.Fatalf("chain ignored the edited three-step length: %+v", switches)
	}
}

func TestChainRenderIsBlockInvariant(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices, cfg.MaxBlock = 1, 1, 256
	cfg.Patterns = []PatternBank{{}}
	for slot, length := range []uint8{2, 3} {
		cfg.Patterns[0].Slots[slot] = seq.Pattern{Len: length, GatePercent: 55, Seed: cfg.Seed}
		cfg.Patterns[0].Slots[slot].Steps[0] = packedNote(t, uint8(45+7*slot))
	}
	makeEngine := func() *Engine {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		if !e.PushBatch([]cmd.Command{chainCommand(0, 0, 0, 2), chainCommand(0, 1, 1, 1), {Op: cmd.OpPlay, Track: 0xff}}) {
			t.Fatal("chain setup rejected")
		}
		return e
	}
	a, b := makeEngine(), makeEngine()
	var aL, aR [128]float32
	var bL, bR [256]float32
	for block := 0; block < 600; block += 2 {
		b.Render(bL[:], bR[:])
		for half := 0; half < 2; half++ {
			a.Render(aL[:], aR[:])
			for frame := range aL {
				index := half*128 + frame
				if aL[frame] != bL[index] || aR[frame] != bR[index] {
					t.Fatalf("chain audio depends on block size at block %d frame %d", block+half, frame)
				}
			}
		}
	}
}

func TestChainRenderDoesNotAllocate(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 16, MaxVoices: 16, BPMMilli: 120_000, Seed: 42, Patterns: make([]PatternBank, 16)}
	var commands [33]cmd.Command
	for track := 0; track < 16; track++ {
		cfg.Track[track].Kind = VoiceAcid
		for slot := 0; slot < 2; slot++ {
			cfg.Patterns[track].Slots[slot] = seq.Pattern{Len: 1, GatePercent: 55, Seed: cfg.Seed}
			cfg.Patterns[track].Slots[slot].Steps[0] = packedNote(t, uint8(45+7*slot))
		}
		commands[track*2] = chainCommand(uint8(track), 0, 0, 1)
		commands[track*2+1] = chainCommand(uint8(track), 1, 1, 1)
	}
	commands[32] = cmd.Command{Op: cmd.OpPlay, Track: 0xff}
	e, err := New(cfg)
	if err != nil || !e.PushBatch(commands[:]) {
		t.Fatalf("chain setup rejected: %v", err)
	}
	e.meterRate = 0
	var left, right [128]float32
	var message cmd.Message
	allocs := testing.AllocsPerRun(1000, func() {
		e.Render(left[:], right[:])
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("chain render fault %d", message.A)
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("sixteen-track chain render allocated %.2f objects", allocs)
	}
	t.Logf("sixteen-track chain render: %.2f allocations per 128-frame block", allocs)
}

func TestQuantizedManualSelectionTakesOverChain(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	commands := make([]cmd.Command, 0, 9)
	for slot, note := range []uint8{45, 52, 60} {
		commands = append(commands,
			cmd.Command{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: uint32(slot)},
			cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, note), Arg1: uint32(slot)},
		)
	}
	commands = append(commands, chainCommand(0, 0, 0, 1), chainCommand(0, 1, 1, 1), cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	if !e.PushBatch(commands) {
		t.Fatal("chain setup rejected")
	}
	var left, right [128]float32
	var switches []cmd.Message
	for block := 0; block < 280; block++ {
		if block == 10 && !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 2, Arg0: 1}) {
			t.Fatal("manual selection rejected")
		}
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("chain fault %d", message.A)
			}
			if message.Kind == cmd.Switched {
				switches = append(switches, message)
			}
		}
	}
	if e.patterns[0].active != 2 || e.patterns[0].chainArmed {
		t.Fatalf("manual selection did not take over chain: active=%d armed=%v", e.patterns[0].active, e.patterns[0].chainArmed)
	}
	for _, message := range switches {
		if message.Tick > seq.PPQ && message.A != 2 {
			t.Fatalf("chain resumed after manual selection: %+v", message)
		}
	}
}

func TestChainSwitchPrecedesParameterFaultAtSameTick(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	cfg.Patterns = []PatternBank{{}}
	for slot, note := range []uint8{45, 127} {
		cfg.Patterns[0].Slots[slot] = seq.Pattern{Len: 1, GatePercent: 55, Seed: cfg.Seed}
		cfg.Patterns[0].Slots[slot].Steps[0] = packedNote(t, note)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{
		chainCommand(0, 0, 0, 1),
		chainCommand(0, 1, 1, 1),
		{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: 1 << 16, Arg1: 1, Tick: seq.TicksPerStep},
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		t.Fatal("chain setup rejected")
	}
	var left, right [128]float32
	var relevant []cmd.Message
	for block := 0; block < 50 && !e.faulted; block++ {
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Switched || message.Kind == cmd.Fault {
				relevant = append(relevant, message)
			}
		}
	}
	if len(relevant) != 3 || relevant[0].Kind != cmd.Switched || relevant[0].Tick != 0 ||
		relevant[1].Kind != cmd.Switched || relevant[1].A != 1 || relevant[1].Tick != seq.TicksPerStep ||
		relevant[2].Kind != cmd.Fault || relevant[2].Tick != seq.TicksPerStep {
		t.Fatalf("structural switch did not precede the same-tick parameter fault: %+v", relevant)
	}
}
