package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func TestSongSceneLaunchKeepOffAndLiveOverride(t *testing.T) {
	cfg := testConfig()
	cfg.Scenes = []Scene{
		{Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 0}, {Mode: SceneSlot, Slot: 0}}},
		{Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 0}, {Mode: SceneOff}}},
		{Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 1}, {Mode: SceneKeep}}},
	}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	commands := []cmd.Command{
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 45), Arg1: 0},
		{Op: cmd.OpSetPatternLen, Track: 0, Index: 1, Arg1: 1},
		{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: packedNote(t, 52), Arg1: 1},
		{Op: cmd.OpSetPatternLen, Track: 1, Index: 1, Arg1: 0},
		{Op: cmd.OpSetStep, Track: 1, Index: 0, Arg0: packedNote(t, 0), Arg1: 0},
		{Op: cmd.OpPlay, Track: 0xff},
	}
	if !e.PushBatch(commands) {
		t.Fatal("arrangement setup rejected")
	}
	var left, right [128]float32
	var liveOverride, songResume, offDrums, drumAfterOff bool
	for block := 0; block < 1600; block++ {
		if block == 20 {
			if !e.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: 2, Arg0: 1}) {
				t.Fatal("live scene launch rejected")
			}
		}
		e.Render(left[:], right[:])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("arrangement fault %d at tick %d", message.A, message.Tick)
			}
			liveOverride = liveOverride || message.Kind == cmd.NoteOn && message.Track == 0 && message.A == 52 && message.Tick == seq.PPQ
			songResume = songResume || message.Kind == cmd.NoteOn && message.Track == 0 && message.A == 45 && message.Tick == seq.TicksPerBar
			offDrums = offDrums || message.Kind == cmd.Switched && message.Track == 1 && message.A == 0xffff && message.Tick == seq.TicksPerBar
			drumAfterOff = drumAfterOff || message.Kind == cmd.NoteOn && message.Track == 1 && message.Tick >= seq.TicksPerBar
		}
	}
	if !liveOverride || !songResume || !offDrums || drumAfterOff {
		t.Fatalf("arrangement transitions: live=%v resume=%v off=%v drumAfterOff=%v", liveOverride, songResume, offDrums, drumAfterOff)
	}
	if e.transport.Playing() || e.transport.Tick() != 2*seq.TicksPerBar {
		t.Fatalf("song did not stop at two bars: playing=%v tick=%d", e.transport.Playing(), e.transport.Tick())
	}
}

func TestInvalidArrangementRejected(t *testing.T) {
	cfg := testConfig()
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}}
	if _, err := New(cfg); err == nil {
		t.Fatal("song with missing scene was accepted")
	}
	cfg.Scenes = []Scene{{Track: [16]SceneBinding{{Mode: SceneSlot, Slot: 16}}}}
	if _, err := New(cfg); err == nil {
		t.Fatal("invalid slot binding was accepted")
	}
}

func TestSimultaneousDrumLanesShareOneStep(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 6}
	cfg.Track[0].Kind = VoiceDrums
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	commands := []cmd.Command{{Op: cmd.OpSetPatternLen, Track: 0, Index: 1}}
	for lane := uint8(0); lane < 6; lane++ {
		step, err := seq.PackStep(seq.Step{Note: lane, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 0, Arg0: step})
	}
	commands = append(commands, cmd.Command{Op: cmd.OpSelectPattern, Track: 0}, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	if !e.PushBatch(commands) {
		t.Fatal("drum pattern batch rejected")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	var hits []uint16
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatalf("drum pattern fault %d", message.A)
		}
		if message.Kind == cmd.NoteOn {
			hits = append(hits, message.A)
		}
	}
	want := []uint16{0, 1, 3, 2, 4, 5} // Open hat before closed hat for same-sample choke.
	if len(hits) != len(want) {
		t.Fatalf("simultaneous drum hits: %v", hits)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Fatalf("drum hit order: %v, want %v", hits, want)
		}
	}
}
