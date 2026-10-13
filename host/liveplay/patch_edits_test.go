package liveplay

import (
	"encoding/binary"
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// editScore is one acid track that starts in a silent slot 0. Slot 1 plays a
// note on every step. Scene "main" binds slot 0, and the song plays it.
func editScore(t *testing.T) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000, Seed: 7, LoopSong: true}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Patterns = []engine.PatternBank{{}}
	note, err := seq.PackStep(seq.Step{Note: 45, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
	if err != nil {
		t.Fatal(err)
	}
	for slot := range 2 {
		cfg.Patterns[0].Slots[slot] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
	}
	for step := range 16 {
		cfg.Patterns[0].Slots[1].Steps[step] = note
	}
	cfg.Scenes = []engine.Scene{{Track: [engine.MaxTracks]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 0}}}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 8}}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "edit", Tracks: []TrackSlots{{ID: "bass"}}, SceneIDs: []string{"main"}}
}

// readFrames returns the left channel of the next frames of audio.
func readFrames(t *testing.T, p *Player, frames int) []float32 {
	t.Helper()
	raw := make([]byte, frames*8)
	if _, err := io.ReadFull(p, raw); err != nil {
		t.Fatal(err)
	}
	out := make([]float32, frames)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*8:]))
	}
	return out
}

func firstSound(samples []float32) int {
	for i, sample := range samples {
		if sample != 0 {
			return i
		}
	}
	return -1
}

func TestStepPatchLandsAtTheNextStepBoundary(t *testing.T) {
	p, err := New(editScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	note, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
	arena := NewPatchArena(2)
	batch := arena.Begin(1)
	batch.Step(target, 0, 2, note)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	// The patch drains at frame 0, so it lands at tick 240; slot 0 step 2 sounds
	// at tick 480, which is frame 12,000 at 120 BPM.
	samples := readFrames(t, p, 24_000)
	if got := p.LandedRevision(); got != 1 {
		t.Fatalf("landed revision %d", got)
	}
	if first := firstSound(samples); first < 12_000 || first > 12_400 {
		t.Fatalf("patched step first sounded at frame %d, want the step at frame 12,000", first)
	}
	control, err := New(editScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if first := firstSound(readFrames(t, control, 24_000)); first >= 0 {
		t.Fatalf("silent slot sounded without a patch at frame %d", first)
	}
}

func TestSceneBindingPatchChangesTheNextLaunch(t *testing.T) {
	run := func(patched bool) []float32 {
		p, err := New(editScore(t), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if patched {
			target, err := p.ResolvePreviewTrack("bass")
			if err != nil {
				t.Fatal(err)
			}
			arena := NewPatchArena(2)
			batch := arena.Begin(1)
			batch.SceneBinding(target, 0, engine.SceneBinding{Mode: engine.SceneSlot, Slot: 1})
			if err := p.Patch(batch); err != nil {
				t.Fatal(err)
			}
		}
		_ = readFrames(t, p, blockFrames)
		if patched && p.LandedRevision() != 1 {
			t.Fatalf("binding patch did not land: %d", p.LandedRevision())
		}
		if err := p.LaunchScene("main"); err != nil {
			t.Fatal(err)
		}
		return readFrames(t, p, 3*96_000)
	}
	if first := firstSound(run(false)); first >= 0 {
		t.Fatalf("an unpatched launch sounded at frame %d", first)
	}
	if first := firstSound(run(true)); first < 96_000-blockFrames {
		t.Fatalf("patched binding sounded at frame %d, before the bar-quantized launch", first)
	} else if first > 96_000+4_000 {
		t.Fatalf("patched binding sounded late at frame %d", first)
	}
}

func TestStepAndBindingPatchesRejectStaleOrUnknownTargets(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(3)
	bass, _ := p.ResolvePreviewTrack("bass")
	if err := p.Offer(reorderedMeterScore(t, "remove", [2]string{"lead", "pad"}, -12, -9)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	note, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
	lead, _ := p.ResolvePreviewTrack("lead")
	batch := arena.Begin(1)
	batch.Step(lead, 0, 1, note)
	batch.Step(bass, 0, 1, note) // bass left the playing score
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if p.LandedRevision() != 0 || arena.Free() != 3 {
		t.Fatalf("stale step batch landed (%d) or leaked (%d free)", p.LandedRevision(), arena.Free())
	}
	batch = arena.Begin(2)
	batch.SceneBinding(lead, 99, engine.SceneBinding{Mode: engine.SceneOff}) // the engine has no scene 99
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if p.LandedRevision() != 0 || arena.Free() != 3 {
		t.Fatalf("unknown scene landed or leaked: revision %d, %d free", p.LandedRevision(), arena.Free())
	}
	batch = arena.Begin(3)
	batch.SceneBinding(PreviewTarget{}, 0, engine.SceneBinding{Mode: engine.SceneOff})
	if err := p.Patch(batch); err == nil {
		t.Fatal("a binding without a resolved track was accepted")
	}
	if p.current.Engine.TrackCount() != 2 {
		t.Fatal("test score changed")
	}
	if renderPreview(t, p, blockFrames); p.fault != nil {
		t.Fatalf("a refused patch latched a fault: %v", p.fault)
	}
}

// A step patch drained inside a 60 BPM scene stamps its tick with the live
// tempo, so it lands at the next step there and the edited step sounds in
// the same bar.
func TestStepPatchUsesTheLiveTempoAfterASceneTempoChange(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000, Seed: 7, LoopSong: true}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Patterns = []engine.PatternBank{{}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
	cfg.Scenes = []engine.Scene{
		{Track: [engine.MaxTracks]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 0}}},
		{Settings: []engine.SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 60}}},
	}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 4}}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "tempo", Tracks: []TrackSlots{{ID: "bass"}}}, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	const patchFrame = 156_160 // block 610: 60,160 frames into the 60 BPM bar
	_ = readFrames(t, p, patchFrame)
	if p.BPMMilli() != 60_000 {
		t.Fatalf("tempo %d", p.BPMMilli())
	}
	target, _ := p.ResolvePreviewTrack("bass")
	note, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
	batch := NewPatchArena(1).Begin(1)
	batch.Step(target, 0, 8, note)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	// Step 8 of the bar that began at frame 96,000 sounds at tick 5,760,
	// frame 96,000 + 1,920*50 = 192,000.
	samples := readFrames(t, p, 192_000+4_000-patchFrame)
	first := firstSound(samples)
	if first < 0 || patchFrame+first < 192_000 || patchFrame+first > 192_400 {
		t.Fatalf("edited step first sounded at frame %d, want 192,000", patchFrame+first)
	}
}
