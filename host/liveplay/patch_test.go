package liveplay

import (
	"fmt"
	"io"
	"sync"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
)

func TestPatchBeforeStructuralOfferCannotOverwriteReplacement(t *testing.T) {
	makeScore := func(t *testing.T, decay float32, tracks int) Score {
		cfg := previewParameterConfig(t, engine.VoiceDrums)
		cfg.Tracks, cfg.MaxVoices = tracks, 32
		cfg.Track[0].Drums[drum.BD].Decay = float64(decay) / 1000
		cfg.Scenes = []engine.Scene{{Settings: []engine.SceneSetting{{Track: 0, ID: kernel.ParamMixGain, Value: -6}}}, {}}
		cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
		if tracks == 2 {
			cfg.Track[1] = cfg.Track[0]
		}
		created, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		ids := []TrackSlots{{ID: "drums"}}
		if tracks == 2 {
			ids = append(ids, TrackSlots{ID: "added"})
		}
		return Score{Engine: created, SampleRate: 48_000, BPMMilli: cfg.BPMMilli,
			Tracks: ids, SceneIDs: []string{"first", "last"},
			Song:       []SongEntry{{Scene: "first", StartBar: 1}, {Scene: "last", StartBar: 2}},
			Parameters: []ParameterValue{{Track: 0, ID: kernel.ParamDrumBdDecay, Value: decay}}}
	}
	for _, later := range []bool{false, true} {
		t.Run(fmt.Sprintf("later-patch-%v", later), func(t *testing.T) {
			p, err := New(makeScore(t, 80, 1), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			arena := NewPatchArena(2)
			old := arena.Begin(1)
			old.SetParam(0, kernel.ParamDrumBdDecay, 120)
			if err := p.Patch(old); err != nil {
				t.Fatal(err)
			}
			var laterBatch *Patch
			if later {
				// Builder revision and capture time do not order publications.
				laterBatch = arena.Begin(0)
				laterBatch.SetParam(0, kernel.ParamDrumBdDecay, 240)
			}
			next := makeScore(t, 200, 2)
			if err := p.Offer(next); err != nil {
				t.Fatal(err)
			}
			want := float32(200)
			if later {
				if err := p.Patch(laterBatch); err != nil {
					t.Fatal(err)
				}
				want = 240
			}
			var pcm [blockFrames * 8]byte
			if err := p.StartSongEntry(0, "first"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Read(pcm[:]); err != nil {
				t.Fatal(err)
			}
			check := func() {
				validator := next.Engine.PreviewParamValidator(0)
				value, ok := validator.CommittedValue(kernel.ParamDrumBdDecay)
				got := float32(value)
				if !ok || got != want {
					t.Fatalf("engine decay %g/%v, want newest published value %g", got, ok, want)
				}
				got, ok = p.CommittedValue(0, kernel.ParamDrumBdDecay)
				if !ok || got != want {
					t.Fatalf("cancellation value %g/%v, want %g", got, ok, want)
				}
			}
			check()
			if err := p.StartSongEntry(1, "last"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Read(pcm[:]); err != nil {
				t.Fatal(err)
			}
			check() // Saved reconstruction defaults must also survive a seek.
			version, err := p.SetPreview(0, kernel.ParamDrumBdDecay, 300)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Read(pcm[:]); err != nil {
				t.Fatal(err)
			}
			if err := p.CancelPreview(version); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Read(pcm[:]); err != nil {
				t.Fatal(err)
			}
			check()
			if arena.Free() != 2 {
				t.Fatal("superseded patches did not recycle")
			}
		})
	}
}

func TestPatchAndOfferConcurrentPublicationOrder(t *testing.T) {
	p, err := New(previewRenderScoreTracks(t, -6, 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	const producers = 8
	arena := NewPatchArena(producers)
	var scores [producers]Score
	var batches [producers]*Patch
	var errors [producers]error
	for i := range scores {
		value := -float32(i + 1)
		scores[i] = previewRenderScoreTracks(t, value, 3)
		batches[i] = arena.Begin(uint64(i))
		batches[i].SetParam(0, kernel.ParamMixGain, value)
	}
	var done sync.WaitGroup
	for i := range scores {
		done.Go(func() {
			if err := p.Offer(scores[i]); err != nil {
				errors[i] = err
				return
			}
			errors[i] = p.Patch(batches[i])
		})
	}
	done.Wait()
	for _, err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	// The test owns the reader and inspects the publication records before
	// allowing any batch to recycle. Reinsert the same pending score unchanged.
	next := <-p.offers
	p.offers <- next
	newest := next.publication
	want := next.Parameters[0].Value
	for i, batch := range batches {
		if batch.publication > newest {
			newest, want = batch.publication, -float32(i+1)
		}
	}
	if err := p.StartSongEntry(0, "preview"); err != nil {
		t.Fatal(err)
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if got, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || got != want {
		t.Fatalf("concurrent publications committed %g/%v, want sequence %d value %g", got, ok, newest, want)
	}
	if arena.Free() != producers {
		t.Fatal("concurrent patches did not recycle")
	}
}

func TestReplacementSupersedesDeferredPatchesWithoutAllocation(t *testing.T) {
	for _, songStart := range []bool{false, true} {
		t.Run(fmt.Sprintf("song-start-%v", songStart), func(t *testing.T) {
			// Prepare each publication outside the measured reader. Each run
			// activates a real replacement with a deferred head, queued older
			// batches, and a later patch targeting the surviving incarnation.
			var players [21]*Player
			var arenas [21]*PatchArena
			var pcm [blockFrames * 8]byte
			for i := range players {
				p, err := New(previewRenderScoreTracks(t, -6, 2), 48_000)
				if err != nil {
					t.Fatal(err)
				}
				players[i] = p
				t.Cleanup(p.Close)
				arena := NewPatchArena(10)
				arenas[i] = arena
				for revision := uint64(1); revision <= 9; revision++ {
					batch := arena.Begin(revision)
					for repeat := 0; repeat < 16; repeat++ {
						batch.SetParam(0, kernel.ParamMixGain, -3)
						batch.SetParam(0, kernel.ParamMixPan, -1)
						batch.SetParam(0, kernel.ParamMixSendA, 0)
						batch.SetParam(0, kernel.ParamMixSendB, 0)
					}
					if err := p.Patch(batch); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := p.Read(pcm[:]); err != nil {
					t.Fatal(err)
				}
				if p.deferredPatch == nil {
					t.Fatal("burst did not defer")
				}
				if err := p.Offer(previewRenderScoreTracks(t, -12, 3)); err != nil {
					t.Fatal(err)
				}
				later := arena.Begin(10)
				later.SetParam(0, kernel.ParamMixGain, 0)
				if err := p.Patch(later); err != nil {
					t.Fatal(err)
				}
				if songStart {
					if err := p.StartSongEntry(0, "preview"); err != nil {
						t.Fatal(err)
					}
				} else {
					// The test owns the reader and advances it to its next bar.
					p.sample = p.nextBarSample
				}
			}
			// AllocsPerRun counts mallocs from every goroutine. Each player's
			// off-thread loudness meter allocates a snapshot every 100 ms, so a
			// slow window under load would count meter work, not render work.
			// Stop the meters; Read still pushes blocks into their rings.
			for _, p := range players {
				p.loudness.close()
			}
			i := 0
			allocs := testing.AllocsPerRun(20, func() {
				p := players[i]
				if _, err := p.Read(pcm[:]); err != nil {
					panic(err)
				}
				if err := p.StartSongEntry(0, "preview"); err != nil {
					panic(err)
				}
				if _, err := p.Read(pcm[:]); err != nil {
					panic(err)
				}
				if p.LandedRevision() != 10 || arenas[i].Free() != 10 {
					panic("publication order or recycling failed")
				}
				if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || value != 0 {
					panic("newest reconstruction value lost")
				}
				i++
			})
			if allocs != 0 {
				t.Fatalf("replacement/deferred recycling allocated %g", allocs)
			}
		})
	}

}

func TestPatchCapacityBurstDefersWithoutFaultOrAllocation(t *testing.T) {
	cfg := previewParameterConfig(t, engine.VoiceGraph)
	cfg.Tracks, cfg.MaxVoices = 16, 16
	tracks := make([]TrackSlots, 16)
	for i := range tracks {
		cfg.Track[i] = cfg.Track[0]
		tracks[i] = TrackSlots{ID: fmt.Sprintf("drums%d", i)}
	}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: tracks}, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(16)
	for revision := uint64(1); revision <= 9; revision++ {
		batch := arena.Begin(revision)
		for track := uint8(0); track < 16; track++ {
			for _, id := range []kernel.ParamID{kernel.ParamMixGain, kernel.ParamMixPan, kernel.ParamMixSendA, kernel.ParamMixSendB} {
				batch.SetParam(track, id, 0)
			}
		}
		if err := p.Patch(batch); err != nil {
			t.Fatal(err)
		}
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatalf("nine admitted batches faulted: %v", err)
	}
	if p.LandedRevision() >= 9 {
		t.Fatal("capacity burst was not deferred")
	}
	for block := 0; block < 16 && p.LandedRevision() < 9; block++ {
		if _, err := p.Read(pcm[:]); err != nil {
			t.Fatal(err)
		}
	}
	if p.LandedRevision() != 9 || arena.Free() != 16 {
		t.Fatal("deferred patches did not land/recycle")
	}
	// Count the patch/render path without the off-thread loudness snapshots,
	// which allocate while a slow or race-instrumented burst is measured.
	// Read still publishes its audio blocks to the stopped meter's ring.
	p.loudness.close()
	base := uint64(9)
	allocs := testing.AllocsPerRun(50, func() {
		for revision := base + 1; revision <= base+9; revision++ {
			batch := arena.Begin(revision)
			for track := uint8(0); track < 16; track++ {
				batch.SetParam(track, kernel.ParamMixGain, 0)
				batch.SetParam(track, kernel.ParamMixPan, 0)
				batch.SetParam(track, kernel.ParamMixSendA, 0)
				batch.SetParam(track, kernel.ParamMixSendB, 0)
			}
			if err := p.Patch(batch); err != nil {
				panic(err)
			}
		}
		for block := 0; block < 8; block++ {
			if _, err := p.Read(pcm[:]); err != nil {
				panic(err)
			}
		}
		base += 9
		if p.LandedRevision() != base || arena.Free() != 16 {
			panic("deferred burst did not finish")
		}
	})
	if allocs != 0 {
		t.Fatalf("capacity burst allocated %g", allocs)
	}
}

func TestPatchSeekReconstructionDoesNotAllocate(t *testing.T) {
	score := previewRenderScore(t, -6)
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	arena := NewPatchArena(1)
	batch := arena.Begin(1)
	batch.SetParam(0, kernel.ParamMixGain, -3)
	if err := p.Patch(batch); err != nil {
		t.Fatal(err)
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	index := 0
	allocs := testing.AllocsPerRun(50, func() {
		index = 2 - index
		if err := p.StartSongEntry(index, []string{"preview", "later", "last"}[index]); err != nil {
			panic(err)
		}
		if _, err := p.Read(pcm[:]); err != nil {
			panic(err)
		}
		want := float32(-3)
		if index == 2 {
			want = -9
		}
		if got, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || got != want {
			panic("seek reconstruction diverged")
		}
	})
	if allocs != 0 {
		t.Fatalf("seek reconstruction allocated %g", allocs)
	}
}

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
