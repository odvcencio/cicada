package liveplay

import (
	"io"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
)

func TestPatchAppliesAtNextBlockWithoutAllocating(t *testing.T) {
	p, err := New(meterScore(t, "patch", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(64)
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	var pcm [blockFrames * 8]byte
	revision := uint64(0)
	allocs := testing.AllocsPerRun(50, func() {
		revision++
		batch := arena.Begin(revision)
		if batch == nil {
			panic("arena exhausted")
		}
		batch.SetResolvedParam(target, kernel.ParamMixGain, 0)
		if err := p.Patch(batch); err != nil {
			panic(err)
		}
		if _, err := p.Read(pcm[:]); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("patch apply allocated %g", allocs)
	}
	if _, err := io.CopyN(io.Discard, p, 48_000*8); err != nil {
		t.Fatal(err)
	}
	frame := <-p.Meters()
	if frame.Tracks[0].Peak < .95 {
		t.Fatalf("gain patch did not land: peak %g", frame.Tracks[0].Peak)
	}
	if got := p.LandedRevision(); got != revision {
		t.Fatalf("landed revision %d, want %d", got, revision)
	}
	if arena.Free() != 64 {
		t.Fatalf("arena did not recycle applied batches: %d free", arena.Free())
	}
}

func TestPatchSharesPreparedRecipeValidation(t *testing.T) {
	cfg := previewParameterConfig(t, engine.VoiceDrums)
	kit := new([drum.LaneCount]engine.KitLaneBinding)
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		kit[lane] = engine.KitLaneBinding{Kind: engine.KitLaneBuiltin, Recipe: lane}
	}
	kit[drum.BD].Recipe = drum.SD
	cfg.Track[0].Kit = kit
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "bass"}}}, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(1)
	target, _ := p.ResolvePreviewTrack("bass")
	for _, id := range []kernel.ParamID{kernel.ParamDrumBdTune, kernel.ParamDrumSdTune} {
		value := float32(55)
		if id == kernel.ParamDrumSdTune {
			value = .7
		}
		batch := arena.Begin(1)
		batch.SetResolvedParam(target, id, value)
		if batch.Err() == nil || p.Patch(batch) == nil {
			t.Fatalf("recipe-invalid %s accepted", kernel.Params[id].Name)
		}
	}
	batch := arena.Begin(2)
	batch.SetResolvedParam(target, kernel.ParamDrumBdDecay, 100)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if !created.Playing() || p.LandedRevision() != 2 || arena.Free() != 1 {
		t.Fatal("valid prepared recipe patch did not land")
	}
}

func TestPatchPreviewRetirementDoesNotAllocate(t *testing.T) {
	p, err := New(meterScore(t, "patch", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(1)
	target, _ := p.ResolvePreviewTrack("bass")
	var snapshots [51]*liveOverrides
	for i := range snapshots {
		if _, err := p.SetResolvedPreview(target, kernel.ParamMixGain, 0); err != nil {
			t.Fatal(err)
		}
		snapshots[i] = p.overrides.Load()
	}
	var pcm [blockFrames * 8]byte
	iteration := 0
	allocs := testing.AllocsPerRun(50, func() {
		p.overrides.Store(snapshots[iteration])
		batch := arena.Begin(uint64(iteration + 1))
		batch.SetResolvedParam(target, kernel.ParamMixGain, 0)
		if err := p.Patch(batch); err != nil {
			panic(err)
		}
		if _, err := p.Read(pcm[:]); err != nil {
			panic(err)
		}
		if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
			panic("matching override survived")
		}
		iteration++
	})
	if allocs != 0 {
		t.Fatalf("patch retirement allocated %g", allocs)
	}
}

func TestPatchResolvesReordersAndRejectsReaddedIncarnationsAtomically(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(2)
	target, _ := p.ResolvePreviewTrack("bass")
	if err := p.Offer(reorderedMeterScore(t, "reorder", [2]string{"lead", "bass"}, -12, -6)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	batch := arena.Begin(1)
	batch.SetResolvedParam(target, kernel.ParamMixGain, -3)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if got, _ := p.CommittedValue(1, kernel.ParamMixGain); got != -3 {
		t.Fatalf("reordered bass commit %g", got)
	}
	if got, _ := p.CommittedValue(0, kernel.ParamMixGain); got != -12 {
		t.Fatalf("patch changed lead: %g", got)
	}
	if err := p.Offer(reorderedMeterScore(t, "remove", [2]string{"lead", "pad"}, -12, -9)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	if err := p.Offer(reorderedMeterScore(t, "readd", [2]string{"lead", "bass"}, -12, -6)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	lead, _ := p.ResolvePreviewTrack("lead")
	batch = arena.Begin(2)
	batch.SetResolvedParam(lead, kernel.ParamMixGain, 0)
	batch.SetResolvedParam(target, kernel.ParamMixGain, 0)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if p.LandedRevision() != 1 || arena.Free() != 2 {
		t.Fatal("stale batch landed or leaked")
	}
	if got, _ := p.CommittedValue(0, kernel.ParamMixGain); got != -12 {
		t.Fatal("stale batch partially applied")
	}
	if got, _ := p.CommittedValue(1, kernel.ParamMixGain); got != -6 {
		t.Fatal("old incarnation reached re-added bass")
	}
}

func TestPatchCapacityAndCloseReturnAllBatches(t *testing.T) {
	p, err := New(meterScore(t, "patch", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(2)
	batch := arena.Begin(1)
	for i := 0; i < 65; i++ {
		batch.SetParam(0, kernel.ParamMixGain, 0)
	}
	if batch.Err() == nil || p.Patch(batch) == nil {
		t.Fatal("overflowed batch accepted")
	}
	batch = arena.Begin(2)
	batch.SetParam(0, kernel.ParamMixGain, -3)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	p.Close()
	if arena.Free() != 2 {
		t.Fatal("Close retained queued batches")
	}
}

func TestPatchRetiresMatchingPreviewAndCancelRestoresNewCommit(t *testing.T) {
	p, err := New(meterScore(t, "patch", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(1)
	target, _ := p.ResolvePreviewTrack("bass")
	version, err := p.SetResolvedPreview(target, kernel.ParamMixGain, 0)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	batch := arena.Begin(1)
	batch.SetResolvedParam(target, kernel.ParamMixGain, 0)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("matching preview did not retire")
	}
	if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || value != 0 {
		t.Fatalf("committed value %g, present %v", value, ok)
	}
	p.CancelPreviewWait(version)
	version, err = p.SetResolvedPreview(target, kernel.ParamMixGain, -3)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	p.CancelPreviewWait(version)
	renderPreview(t, p, 4096)
	assertPreviewGain(t, p, 0)
}

func TestPatchRejectsInvalidBatchAndRecyclesPressure(t *testing.T) {
	p, err := New(meterScore(t, "patch", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(34)
	target, _ := p.ResolvePreviewTrack("bass")
	bad := arena.Begin(1)
	bad.SetResolvedParam(target, kernel.ParamMixGain, 999)
	if bad.Err() == nil || p.Patch(bad) == nil {
		t.Fatal("invalid patch accepted")
	}
	for i := 0; i < 33; i++ {
		batch := arena.Begin(uint64(i + 2))
		if batch == nil {
			t.Fatal("unexpected arena exhaustion")
		}
		batch.SetResolvedParam(target, kernel.ParamMixGain, -3)
		err := p.Patch(batch)
		if (i == 32) != (err != nil) {
			t.Fatalf("mailbox admission %d: %v", i, err)
		}
	}
	renderPreview(t, p, blockFrames)
	if arena.Free() != 34 {
		t.Fatalf("rejected/applied batches leaked: %d", arena.Free())
	}
	p.Close()
	batch := arena.Begin(99)
	batch.SetResolvedParam(target, kernel.ParamMixGain, 0)
	if p.Patch(batch) == nil {
		t.Fatal("closed player accepted a patch")
	}
	if arena.Free() != 34 {
		t.Fatal("closed admission leaked batch")
	}
}
