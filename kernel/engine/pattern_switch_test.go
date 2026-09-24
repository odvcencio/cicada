package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func TestSlideCarriesAcrossQuantizedPatternSwitch(t *testing.T) {
	for _, mode := range []string{"pattern", "scene", "song"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig()
			cfg.Tracks, cfg.MaxVoices = 1, 1
			if mode != "pattern" {
				cfg.Scenes = []Scene{
					{Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 0}}},
					{Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 1}}},
				}
			}
			if mode == "song" {
				cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
			}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.meterRate = 0
			last, err := seq.PackStep(seq.Step{Note: 45, Gate: true, Slide: true, Ratchet: 1, Probability: 100, Velocity: 100})
			if err != nil {
				t.Fatal(err)
			}
			target, err := seq.PackStep(seq.Step{Note: 52, Gate: true, Accent: true, Ratchet: 1, Probability: 100, Velocity: 100})
			if err != nil {
				t.Fatal(err)
			}
			commands := []cmd.Command{
				{Op: cmd.OpSetStep, Track: 0, Index: 15, Arg0: last, Arg1: 0},
				{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: target, Arg1: 1},
			}
			if mode != "song" {
				commands = append(commands, cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 0})
			}
			commands = append(commands, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			if !e.PushBatch(commands) {
				t.Fatal("project command batch rejected")
			}
			var left, right [128]float32
			for block := 0; block < 750; block++ {
				if block == 1 && mode != "song" {
					switchCommand := cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg0: 2}
					if mode == "scene" {
						switchCommand = cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 1, Arg0: 2}
					}
					if !e.Push(switchCommand) {
						t.Fatal("quantized switch rejected")
					}
				}
				e.Render(left[:], right[:])
				if block == 740 && e.patterns[0].playingNote == 0 {
					t.Fatal("sliding note released before the target pattern's first onset")
				}
			}
			e.Render(left[:], right[:]) // sample 96000, bar boundary
			if e.patterns[0].active != 1 || e.patterns[0].playingNote == 0 {
				t.Fatal("new pattern did not take over at the bar boundary")
			}
			if got := e.voices[0].acid.AccentStrength(); got != 0 {
				t.Fatalf("sliding target retriggered the accent envelope: %v", got)
			}
			var seen bool
			var message cmd.Message
			for e.Poll(&message) {
				if message.Kind == cmd.NoteOn && message.Tick == seq.TicksPerBar && message.A == 52 {
					seen = true
				}
				if message.Kind == cmd.Fault {
					t.Fatalf("engine fault %d", message.A)
				}
			}
			if !seen {
				t.Fatal("target note onset was not emitted")
			}
		})
	}
}

func TestSwitchToRestReleasesOutgoingSlideAtNormalGate(t *testing.T) {
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
	if !e.PushBatch([]cmd.Command{
		{Op: cmd.OpSetStep, Track: 0, Index: 15, Arg0: last, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 48), Arg1: 0},
		{Op: cmd.OpSelectPattern, Track: 0, Index: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}) {
		t.Fatal("project command batch rejected")
	}
	var left, right [128]float32
	for block := 0; block < 740; block++ {
		if block == 1 && !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg0: 2}) {
			t.Fatal("quantized switch rejected")
		}
		e.Render(left[:], right[:])
	}
	if e.patterns[0].playingNote != 0 {
		t.Fatal("outgoing note remained gated after its normal 55% gate")
	}
	var offTick int64 = -1
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.NoteOff {
			offTick = message.Tick
		}
		if message.Kind == cmd.Fault {
			t.Fatalf("engine fault %d", message.A)
		}
	}
	if offTick != 3600+132 {
		t.Fatalf("outgoing note released at tick %d, want 3732", offTick)
	}
}

func TestSwingSwitchToRestReleasesAfterBoundaryAtNormalGate(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	step, err := seq.PackStep(seq.Step{Note: 45, Gate: true, Slide: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, SwingPermille: 500, GatePercent: 100, Seed: cfg.Seed}
	cfg.Patterns[0].Slots[0].Steps[0] = step
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{{Op: cmd.OpSelectPattern, Track: 0, Index: 0}, {Op: cmd.OpPlay, Track: 0xff}}) {
		t.Fatal("project command batch rejected")
	}
	var left, right [128]float32
	for block := 0; block < 100; block++ {
		if block == 1 && !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg0: 3}) {
			t.Fatal("pattern end switch rejected")
		}
		e.Render(left[:], right[:])
	}
	if e.patterns[0].playingNote != 0 {
		t.Fatal("outgoing swung note remained gated after its normal gate")
	}
	var offTick int64 = -1
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.NoteOff {
			offTick = message.Tick
		}
		if message.Kind == cmd.Fault {
			t.Fatalf("engine fault %d", message.A)
		}
	}
	if offTick != 360 {
		t.Fatalf("outgoing swung note released at tick %d, want 360", offTick)
	}
}
