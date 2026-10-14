package liveplay

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/piano"
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

// Native sessions reach 64 tracks; a binding for track index 17 must reach the
// engine, and a scene-binding patch must change that track's next launch.
func TestSceneBindingPatchReachesTracksBeyondSixteen(t *testing.T) {
	const tracks = 20
	if engine.MaxTracks < tracks {
		t.Skip("this build has fewer than 20 tracks")
	}
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: tracks, MaxVoices: tracks, BPMMilli: 120_000, Seed: 7}
	for i := range tracks {
		cfg.Track[i].Kind = engine.VoiceAcid
	}
	cfg.Scenes = []engine.Scene{{}}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	score := Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "wide", SceneIDs: []string{"main"}, Tracks: make([]TrackSlots, tracks)}
	for i := range score.Tracks {
		score.Tracks[i].ID = fmt.Sprintf("t%d", i)
	}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	target, err := p.ResolvePreviewTrack("t17")
	if err != nil || target.track != 17 {
		t.Fatalf("target %+v %v", target, err)
	}
	batch := NewPatchArena(1).Begin(1)
	batch.SceneBinding(target, 0, engine.SceneBinding{Mode: engine.SceneSlot, Slot: 3})
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, p, blockFrames)
	if p.LandedRevision() != 1 || p.fault != nil {
		t.Fatalf("binding for track 17 did not land: revision %d, fault %v", p.LandedRevision(), p.fault)
	}
}

// A replacement can keep a track's ID but change its voice. A clip binding
// built against the old audio track would fault the new acid engine, so the
// batch is refused whole and its revision never reports as landed.
func TestSceneBindingPatchIsCheckedAgainstTheReplacementEngine(t *testing.T) {
	build := func(audio bool) Score {
		cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000, Seed: 7}
		cfg.Track[0].Kind = engine.VoiceAcid
		if audio {
			pcm := make([]float32, 48_000)
			cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceAudio, GainSet: true}
			cfg.Assets = []engine.AudioAsset{{Left: pcm, SampleRate: 48_000}}
			cfg.Clips = []engine.ClipConfig{{EndFrame: int64(len(pcm))}}
		}
		cfg.Scenes = []engine.Scene{{}}
		created, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "swap", Tracks: []TrackSlots{{ID: "bass"}}, SceneIDs: []string{"main"}}
	}
	p, err := New(build(true), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	arena := NewPatchArena(2)
	batch := arena.Begin(1)
	batch.SceneBinding(target, 0, engine.SceneBinding{Mode: engine.SceneClip, Clip: 0})
	if err := p.Offer(build(false)); err != nil { // same ID, now an acid voice
		t.Fatal(err)
	}
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, p, 2*96_000+blockFrames) // past the bar where the replacement lands
	if p.fault != nil {
		t.Fatalf("a stale clip binding stopped the replacement engine: %v", p.fault)
	}
	if p.LandedRevision() != 0 || arena.Free() != 2 {
		t.Fatalf("incompatible batch reported landed (%d) or leaked (%d free)", p.LandedRevision(), arena.Free())
	}
	// The same binding against the audio engine still lands.
	q, err := New(build(true), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	target, _ = q.ResolvePreviewTrack("bass")
	batch = arena.Begin(2)
	batch.SceneBinding(target, 0, engine.SceneBinding{Mode: engine.SceneClip, Clip: 0})
	if err := q.Patch(batch); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, q, blockFrames)
	if q.LandedRevision() != 2 || q.fault != nil {
		t.Fatalf("a valid clip binding did not land: revision %d fault %v", q.LandedRevision(), q.fault)
	}
}

// A step edit submitted in the last step of a listening loop would be stamped
// at the loop end, a tick the loop never renders. It waits for the restart and
// then lands at the first step boundary of the next pass; the audio shows it.
func TestStepPatchInTheLastStepOfALoopSoundsAfterTheRestart(t *testing.T) {
	p, err := New(editScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	loop := PlaybackRange{StartTick: 0, EndTick: seq.TicksPerBar, Loop: true}
	if err := p.PlayRange(loop); err != nil {
		t.Fatal(err)
	}
	const barFrames = 96_000                                                // one bar at 120 BPM
	_ = readFrames(t, p, barFrames-6_000+blockFrames-barFrames%blockFrames) // into the last step (tick 3,600 starts at frame 90,000)
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	note, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
	batch := NewPatchArena(1).Begin(1)
	batch.Step(target, 0, 2, note)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	read := barFrames - 6_000 + blockFrames - barFrames%blockFrames
	rest := readFrames(t, p, 3*barFrames/2)
	if p.LandedRevision() != 1 {
		t.Fatalf("the patch never landed: revision %d", p.LandedRevision())
	}
	// Step 2 of the second pass sounds 480 ticks (12,000 frames) after the restart.
	restart := barFrames - read
	first := firstSound(rest)
	if first < restart+12_000 || first > restart+12_000+600 {
		t.Fatalf("edit first sounded at frame %d of the rest, want about %d (restart %d)", first, restart+12_000, restart)
	}
}

// A range shorter than one step never reaches a step boundary, so an edit
// lands at the current tick instead of waiting for the loop forever.
func TestStepPatchLandsInARangeShorterThanOneStep(t *testing.T) {
	p, err := New(editScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: 100, Loop: true}); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, p, 2*blockFrames)
	target, _ := p.ResolvePreviewTrack("bass")
	note, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
	batch := NewPatchArena(1).Begin(1)
	batch.Step(target, 0, 0, note)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, p, 8*blockFrames)
	if p.LandedRevision() != 1 || p.fault != nil {
		t.Fatalf("edit in a one-step range: revision %d, fault %v", p.LandedRevision(), p.fault)
	}
}

// A replacement can keep a track's ID but change its voice. A chord batch built
// for a piano would fault an acid track (code 39), so the batch is refused whole.
func TestPatternBatchIsCheckedAgainstTheReplacementEngine(t *testing.T) {
	build := func(kind engine.VoiceKind) Score {
		cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: piano.MaxVoices, BPMMilli: 120_000, Seed: 7}
		cfg.Track[0].Kind = kind
		cfg.Patterns = []engine.PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
		created, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "swap", Tracks: []TrackSlots{{ID: "bass"}}}
	}
	note, _ := seq.PackStep(seq.Step{Note: 60, Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	batchFor := func(arena *PatchArena, p *Player, revision uint64) *Patch {
		target, err := p.ResolvePreviewTrack("bass")
		if err != nil {
			t.Fatal(err)
		}
		batch := arena.Begin(revision)
		batch.Step(target, 0, 1, note)
		batch.Chord(target, 0, 1, [4]uint8{60, 64, 67, 0}, 3)
		return batch
	}
	p, err := New(build(engine.VoicePiano), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(2)
	batch := batchFor(arena, p, 1)
	if err := p.Offer(build(engine.VoiceAcid)); err != nil { // same ID, no chord support
		t.Fatal(err)
	}
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, p, 2*96_000+blockFrames)
	if p.fault != nil {
		t.Fatalf("a chord batch stopped the replacement engine: %v", p.fault)
	}
	if p.LandedRevision() != 0 || arena.Free() != 2 {
		t.Fatalf("incompatible batch reported landed (%d) or leaked (%d free)", p.LandedRevision(), arena.Free())
	}
	_ = readFrames(t, p, 4*blockFrames) // playback continues
	// The same batch against the piano still lands.
	q, err := New(build(engine.VoicePiano), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := q.Patch(batchFor(arena, q, 2)); err != nil {
		t.Fatal(err)
	}
	_ = readFrames(t, q, blockFrames)
	if q.LandedRevision() != 2 || q.fault != nil {
		t.Fatalf("a valid chord batch did not land: revision %d fault %v", q.LandedRevision(), q.fault)
	}
}

// For every loop of 1 to 1,300 ticks, at any start and tick, an edit is placed
// at a boundary the range renders, held for a restart that reaches one, or
// applied at once when no boundary is reachable. It is never left waiting on a
// tick the range excludes.
func TestPatchBoundaryIsAlwaysReachableOrApplied(t *testing.T) {
	for start := int64(0); start < 600; start += 7 {
		for length := int64(1); length <= 1_300; length++ {
			for _, loop := range []bool{true, false} {
				r := &PlaybackRange{StartTick: start, EndTick: start + length, Loop: loop}
				for tick := start; tick < r.EndTick; tick += 1 + length/9 {
					stamp, hold, now := patchBoundaryFor(r, tick)
					next := (r.StartTick/seq.TicksPerStep + 1) * seq.TicksPerStep
					switch {
					case now:
						if hold || loop && next < r.EndTick {
							t.Fatalf("%+v tick %d: applied at once though a boundary is reachable", *r, tick)
						}
					case hold:
						if !loop || next >= r.EndTick || stamp < r.EndTick {
							t.Fatalf("%+v tick %d: held without a reachable restart boundary", *r, tick)
						}
					default:
						if stamp <= tick || stamp >= r.EndTick {
							t.Fatalf("%+v tick %d: stamp %d is not inside the range", *r, tick, stamp)
						}
					}
				}
			}
		}
	}
}

// Short and unaligned loops: the step edit lands and sounds, and a parameter
// batch published behind it lands too.
func TestStepEditsLandInShortLoopsWithAParameterBatchBehind(t *testing.T) {
	for _, test := range []struct {
		name       string
		start, end int64
		step       uint8
	}{
		{"one tick", 0, 1, 0}, {"exactly one step", 0, 240, 0}, {"one step and a tick", 0, 241, 0},
		{"later start", 480, 720, 2}, {"unaligned", 7, 300, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := New(editScore(t), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if err := p.PlayRange(PlaybackRange{StartTick: test.start, EndTick: test.end, Loop: true}); err != nil {
				t.Fatal(err)
			}
			_ = readFrames(t, p, 4*blockFrames)
			target, _ := p.ResolvePreviewTrack("bass")
			note, _ := seq.PackStep(seq.Step{Note: 52, Gate: true, Ratchet: 1, Probability: 100, Velocity: 120})
			arena := NewPatchArena(2)
			edit := arena.Begin(1)
			edit.Step(target, 0, test.step, note)
			gain := arena.Begin(2)
			gain.SetResolvedParam(target, kernel.ParamMixGain, -3)
			if err := p.Patch(edit); err != nil {
				t.Fatal(err)
			}
			if err := p.Patch(gain); err != nil {
				t.Fatal(err)
			}
			samples := readFrames(t, p, 96_000)
			if p.LandedRevision() != 2 || arena.Free() != 2 || p.fault != nil {
				t.Fatalf("edit and parameter batch: revision %d, %d free, fault %v", p.LandedRevision(), arena.Free(), p.fault)
			}
			if test.end-test.start == 1 {
				// A loop of 25 frames cannot hold a note. Widen the range to show
				// that the edit reached the engine's pattern.
				if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: 240, Loop: true}); err != nil {
					t.Fatal(err)
				}
				samples = readFrames(t, p, 48_000)
			}
			if firstSound(samples) < 0 {
				t.Fatal("the edited step never sounded")
			}
		})
	}
}

// dependentScore plays one pattern track whose slot 0 holds one gated note at
// step 3 (or step 12, for the chain test).
func dependentScore(t *testing.T, kind engine.VoiceKind, step int, note uint8) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: piano.MaxVoices, BPMMilli: 120_000, Seed: 7, LoopSong: true}
	cfg.Track[0].Kind = kind
	cfg.Patterns = []engine.PatternBank{{}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
	packed, err := seq.PackStep(seq.Step{Note: note, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Patterns[0].Slots[0].Steps[step] = packed
	cfg.Scenes = []engine.Scene{{Track: [engine.MaxTracks]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 0}}}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 8}}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "dependent", Tracks: []TrackSlots{{ID: "bass"}}, SceneIDs: []string{"main"}, Song: []SongEntry{{Scene: "main", StartBar: 1}}}
}

type dependentBatch func(*Patch, PreviewTarget)

func publishDependent(t *testing.T, p *Player, batches ...dependentBatch) *PatchArena {
	t.Helper()
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	arena := NewPatchArena(len(batches))
	for i, build := range batches {
		batch := arena.Begin(uint64(i + 1))
		build(batch, target)
		if err := p.Patch(batch); err != nil { // no render between publications
			t.Fatal(err)
		}
	}
	return arena
}

func stepTo(step, note uint8) dependentBatch {
	packed, _ := seq.PackStep(seq.Step{Note: note, Gate: true, Ratchet: 1, Probability: 100, Velocity: 110})
	return func(b *Patch, target PreviewTarget) { b.Step(target, 0, step, packed) }
}

func transpose(semitones int8) dependentBatch {
	return func(b *Patch, target PreviewTarget) { b.PatternMeta(target, 0, 0, 0, semitones) }
}

func length(n uint8) dependentBatch {
	return func(b *Patch, target PreviewTarget) { b.PatternLen(target, 0, n) }
}

// Batches published back to back are validated in the order the engine runs
// them, so a later batch is judged against the state the earlier ones leave.
func TestDependentBatchesAreValidatedInExecutionOrder(t *testing.T) {
	run := func(t *testing.T, score Score, batches ...dependentBatch) (*Player, *PatchArena, []float32) {
		p, err := New(score, 48_000)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		arena := publishDependent(t, p, batches...)
		samples := readFrames(t, p, 96_000+blockFrames)
		return p, arena, samples
	}
	t.Run("transpose then a note it would push out of range", func(t *testing.T) {
		p, arena, _ := run(t, dependentScore(t, engine.VoiceAcid, 3, 60), transpose(24), stepTo(3, 110))
		if p.fault != nil {
			t.Fatalf("the second batch faulted the engine: %v", p.fault)
		}
		if p.LandedRevision() != 1 || arena.Free() != 2 {
			t.Fatalf("revision %d, %d free: the dependent batch must be refused", p.LandedRevision(), arena.Free())
		}
		_ = readFrames(t, p, 4*blockFrames) // playback continues
	})
	t.Run("note change then a transpose that is valid only after it", func(t *testing.T) {
		// Piano range ends at 108: note 100 plus 24 is out of range, 60 plus 24 is not.
		p, arena, samples := run(t, dependentScore(t, engine.VoicePiano, 3, 100), stepTo(3, 60), transpose(24))
		if p.fault != nil {
			t.Fatalf("engine fault: %v", p.fault)
		}
		if p.LandedRevision() != 2 || arena.Free() != 2 {
			t.Fatalf("revision %d, %d free: both valid batches must land", p.LandedRevision(), arena.Free())
		}
		if firstSound(samples) < 0 {
			t.Fatal("the edited note never sounded")
		}
	})
	t.Run("length, transpose and growth chain", func(t *testing.T) {
		// Step 12 holds note 110. Shortening to 8 keeps it, transposing +24 is fine
		// while it is beyond the length, and growing back would expose 134.
		p, arena, _ := run(t, dependentScore(t, engine.VoiceAcid, 12, 110), length(8), transpose(24), length(16))
		if p.fault != nil {
			t.Fatalf("the growth batch faulted the engine: %v", p.fault)
		}
		if p.LandedRevision() != 2 || arena.Free() != 3 {
			t.Fatalf("revision %d, %d free: only the growth batch is refused", p.LandedRevision(), arena.Free())
		}
	})
	t.Run("the same chain with the retained note replaced first", func(t *testing.T) {
		both := func(b *Patch, target PreviewTarget) {
			stepTo(12, 60)(b, target)
			length(16)(b, target)
		}
		p, arena, _ := run(t, dependentScore(t, engine.VoiceAcid, 12, 110), length(8), transpose(24), both)
		if p.fault != nil {
			t.Fatalf("engine fault: %v", p.fault)
		}
		if p.LandedRevision() != 3 || arena.Free() != 3 {
			t.Fatalf("revision %d, %d free: all three batches are valid", p.LandedRevision(), arena.Free())
		}
	})
}

// A song-entry seek queued between two batches moves the tick under them. The
// second batch is judged in the order the engine will run them, after the seek.
func TestDependentBatchesAcrossAQueuedBackwardSeek(t *testing.T) {
	for _, test := range []struct {
		name         string
		second       dependentBatch
		wantRevision uint64
	}{
		{"dependent batch that would fault is refused", stepTo(3, 110), 1},
		{"dependent batch that is valid lands", stepTo(3, 70), 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := New(dependentScore(t, engine.VoiceAcid, 3, 60), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			_ = readFrames(t, p, 2*96_000) // two bars in, so the seek goes backward
			target, _ := p.ResolvePreviewTrack("bass")
			arena := NewPatchArena(2)
			first := arena.Begin(1)
			transpose(24)(first, target)
			if err := p.Patch(first); err != nil {
				t.Fatal(err)
			}
			if err := p.StartSongEntry(0, "main"); err != nil {
				t.Fatal(err)
			}
			second := arena.Begin(2)
			test.second(second, target)
			if err := p.Patch(second); err != nil {
				t.Fatal(err)
			}
			_ = readFrames(t, p, 3*96_000)
			if p.fault != nil {
				t.Fatalf("the engine faulted: %v", p.fault)
			}
			if p.LandedRevision() != test.wantRevision || arena.Free() != 2 {
				t.Fatalf("revision %d, %d free; want revision %d", p.LandedRevision(), arena.Free(), test.wantRevision)
			}
			_ = readFrames(t, p, 4*blockFrames) // playback continues
		})
	}
}

// Seeks published after a batch was accepted cannot reorder it: edits run in
// arrival order, and a seek first lands the ones ahead of it. Each sequence ends
// with a batch that is valid only in arrival order.
func TestSeeksPublishedAfterAcceptedBatchesNeverReorderThem(t *testing.T) {
	both := func(note uint8, semitones int8) dependentBatch {
		return func(b *Patch, target PreviewTarget) {
			stepTo(3, note)(b, target)
			transpose(semitones)(b, target)
		}
	}
	oneBar := int64(seq.TicksPerBar)
	sequences := map[string]func(t *testing.T, p *Player, arena *PatchArena, target PreviewTarget){
		"backward seek then forward seek": func(t *testing.T, p *Player, arena *PatchArena, target PreviewTarget) {
			publish(t, p, arena, 1, target, both(60, 24)) // waits for its step boundary
			if err := p.StartSongEntry(0, "main"); err != nil {
				t.Fatal(err)
			}
			publish(t, p, arena, 2, target, both(110, 0)) // meets A's +24 in arrival order
			publish(t, p, arena, 3, target, both(70, 0))
			if err := p.StartSongEntry(0, "main"); err != nil {
				t.Fatal(err)
			}
		},
		"backward jump, edit, forward jump": func(t *testing.T, p *Player, arena *PatchArena, target PreviewTarget) {
			if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: 4 * oneBar, Loop: true}); err != nil {
				t.Fatal(err)
			}
			publish(t, p, arena, 1, target, both(60, 24))
			if err := p.PlayRange(PlaybackRange{StartTick: 2 * oneBar, EndTick: 4 * oneBar, Loop: true}); err != nil {
				t.Fatal(err)
			}
			publish(t, p, arena, 2, target, both(110, 0))
			publish(t, p, arena, 3, target, both(70, 0))
		},
		"loop wraps between batches": func(t *testing.T, p *Player, arena *PatchArena, target PreviewTarget) {
			if err := p.PlayRange(PlaybackRange{StartTick: 0, EndTick: oneBar, Loop: true}); err != nil {
				t.Fatal(err)
			}
			publish(t, p, arena, 1, target, both(60, 24))
			_ = readFrames(t, p, 3*96_000+blockFrames) // the loop wraps several times
			publish(t, p, arena, 2, target, both(110, 0))
			publish(t, p, arena, 3, target, both(70, 0))
		},
	}
	for name, run := range sequences {
		t.Run(name, func(t *testing.T) {
			p, err := New(dependentScore(t, engine.VoiceAcid, 3, 100), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			_ = readFrames(t, p, 96_000+blockFrames)
			target, _ := p.ResolvePreviewTrack("bass")
			arena := NewPatchArena(3)
			run(t, p, arena, target)
			_ = readFrames(t, p, 4*96_000)
			if p.fault != nil {
				t.Fatalf("the engine faulted: %v", p.fault)
			}
			// Batches 1 and 3 land; 2 would put note 110 under +24 and is refused.
			if p.LandedRevision() != 3 || arena.Free() != 3 {
				t.Fatalf("revision %d, %d free: want batches 1 and 3 applied and 2 refused", p.LandedRevision(), arena.Free())
			}
		})
	}
}

func publish(t *testing.T, p *Player, arena *PatchArena, revision uint64, target PreviewTarget, build dependentBatch) {
	t.Helper()
	batch := arena.Begin(revision)
	build(batch, target)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
}
