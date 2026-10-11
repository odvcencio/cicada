package liveplay

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
)

func TestPreviewDelayedPublicationCannotReachReaddedTrack(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved-value", true: "newer-gesture"}[newer], func(t *testing.T) {
			p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			captured := p.trackNames.Load()
			original, err := p.SetPreview(0, kernel.ParamMixGain, 0)
			if err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, blockFrames)
			if err := p.Offer(reorderedMeterScore(t, "removed", [2]string{"pad", "lead"}, -9, -12)); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 100_096)
			if _, _, active := p.previewClear(original); active {
				t.Fatal("original preview survived removal")
			}
			if err := p.Offer(reorderedMeterScore(t, "readded", [2]string{"bass", "lead"}, -6, -12)); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 100_096)
			want := -6.
			if newer {
				if _, err := p.SetTrackPreview("bass", kernel.ParamMixGain, -3); err != nil {
					t.Fatal(err)
				}
				want = -3
			}
			// Resume the control call after both activations. It must retain
			// the captured incarnation, including when a newer gesture exists.
			delayed, err := p.setPreview(captured, 0, kernel.ParamMixGain, 0)
			if err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 4096)
			frame := <-p.Meters()
			if got, expected := float64(frame.Tracks[0].Peak), math.Pow(10, want/20); math.Abs(got-expected) > .01 {
				t.Fatalf("re-added bass peak %g, want %g (%g dB)", got, expected, want)
			}
			if _, _, active := p.previewClear(delayed); active {
				t.Fatal("delayed publication was not retired")
			}
			p.CancelPreviewWait(delayed)
			renderPreview(t, p, blockFrames)
			if value, active := p.OverrideValue(0, kernel.ParamMixGain); active != newer || active && value != -3 {
				t.Fatal("old incarnation changed the replacement track's ownership")
			}
		})
	}
}

func TestPreviewResolvedTargetRejectsOtherPlayers(t *testing.T) {
	p, err := New(meterScore(t, "first", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	other, err := New(meterScore(t, "second", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []PreviewTarget{{}, target} {
		before := other.overrides.Load()
		if _, err := other.SetResolvedPreview(invalid, kernel.ParamMixGain, 0); err == nil || other.overrides.Load() != before {
			t.Fatal("invalid target published to a different player")
		}
	}
}

func TestPreviewStaleIncarnationRetirementDoesNotAllocate(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	target, err := p.ResolvePreviewTrack("bass")
	if err != nil {
		t.Fatal(err)
	}
	for _, score := range []Score{
		reorderedMeterScore(t, "removed", [2]string{"pad", "lead"}, -9, -12),
		reorderedMeterScore(t, "readded", [2]string{"bass", "lead"}, -6, -12),
	} {
		if err := p.Offer(score); err != nil {
			t.Fatal(err)
		}
		renderPreview(t, p, 100_096)
	}
	var snapshots [101]*liveOverrides
	for i := range snapshots {
		if _, err := p.SetResolvedPreview(target, kernel.ParamMixGain, 0); err != nil {
			t.Fatal(err)
		}
		snapshots[i] = p.overrides.Load()
	}
	var pcm [blockFrames * 8]byte
	var readErr error
	iteration := 0
	retired := true
	identity := target.names.overrideTrack(target.track)
	allocations := testing.AllocsPerRun(100, func() {
		snapshot := snapshots[iteration]
		p.overrides.Store(snapshot)
		_, readErr = p.Read(pcm[:])
		overrides := snapshot.tracks[identity]
		retired = retired && overrides.state.retiredVersions[kernel.ParamMixGain].Load() == overrides.values[kernel.ParamMixGain].version
		iteration++
	})
	if allocations != 0 || readErr != nil || !retired {
		t.Fatalf("stale retirement allocations=%g, error=%v, retired=%v", allocations, readErr, retired)
	}
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("stale incarnation became active")
	}
	assertPreviewGain(t, p, -6)
}

func TestPreviewCancelAfterReorderAndIndexReuse(t *testing.T) {
	for _, teardown := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "teardown"}[teardown], func(t *testing.T) {
			p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			bass, err := p.SetPreview(0, kernel.ParamMixGain, 0)
			if err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, blockFrames)
			if err := p.Offer(reorderedMeterScore(t, "reordered", [2]string{"lead", "bass"}, -12, -6)); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 100_096)
			lead, err := p.SetPreview(0, kernel.ParamMixGain, -3)
			if err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, blockFrames)
			if teardown {
				p.CancelPreviewWait(bass)
			} else if err := p.CancelPreview(bass); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 4096)
			frame := <-p.Meters()
			for index, gain := range []float64{-3, -6} {
				if got, want := float64(frame.Tracks[index].Peak), math.Pow(10, gain/20); math.Abs(got-want) > .01 {
					t.Fatalf("%s audible peak %g, want %g (%g dB)", frame.TrackIDs[index], got, want, gain)
				}
			}
			if _, active := p.OverrideValue(1, kernel.ParamMixGain); active {
				t.Fatal("cancelled bass preview remains active")
			}
			if value, active := p.OverrideValue(0, kernel.ParamMixGain); !active || value != -3 {
				t.Fatal("cancelling bass changed lead's preview")
			}
			if err := p.CancelPreview(lead); err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, 4096)
			frame = <-p.Meters()
			if math.Abs(float64(frame.Tracks[0].Peak)-math.Pow(10, -12./20)) > .01 {
				t.Fatal("lead cancellation failed to restore its own committed value")
			}
		})
	}
}

func TestPreviewRemovedTrackRetiresBeforeIdentityReturns(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	version, err := p.SetPreview(0, kernel.ParamMixGain, 0)
	if err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, blockFrames)
	if err := p.Offer(reorderedMeterScore(t, "removed", [2]string{"pad", "lead"}, -9, -12)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	if _, _, active := p.previewClear(version); active {
		t.Fatal("removed bass version was not retired")
	}
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("removed bass preview attached to pad")
	}
	if err := p.Offer(reorderedMeterScore(t, "returned", [2]string{"bass", "lead"}, -3, -12)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	frame := <-p.Meters()
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active || math.Abs(float64(frame.Tracks[0].Peak)-math.Pow(10, -3./20)) > .01 {
		t.Fatal("removed preview was resurrected when bass returned")
	}
}

func TestPreviewIdentityPublicationKeepsItsResolvedSnapshot(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Model activation after the control call has resolved and validated bass,
	// but before it publishes. It must retain that identity from its snapshot.
	names := p.trackNames.Load()
	if err := p.Offer(reorderedMeterScore(t, "reordered", [2]string{"lead", "bass"}, -12, -6)); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 100_096)
	version, err := p.setPreview(names, 0, kernel.ParamMixGain, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetTrackPreview("lead", kernel.ParamMixGain, -3); err != nil {
		t.Fatal(err)
	}
	renderPreview(t, p, 4096)
	frame := <-p.Meters()
	if frame.Tracks[1].Peak < .99 || math.Abs(float64(frame.Tracks[0].Peak)-math.Pow(10, -3./20)) > .01 {
		t.Fatal("publication after activation targeted the wrong identity")
	}
	p.noteCommitted(0, kernel.ParamMixGain, 0)
	if _, active := p.OverrideValue(1, kernel.ParamMixGain); !active {
		t.Fatal("lead commit retired bass at its former index")
	}
	p.noteCommitted(1, kernel.ParamMixGain, 0)
	if _, active := p.OverrideValue(1, kernel.ParamMixGain); active {
		t.Fatal("matching bass commit did not retire its identity")
	}
	if _, _, active := p.previewClear(version); active {
		t.Fatal("retired bass version still owns a cancellation")
	}
}

func TestPreviewTopologyChangesAndCancellationDoNotAllocate(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	reordered := reorderedMeterScore(t, "reordered", [2]string{"lead", "bass"}, -12, -6)
	reordered.trackNames = makeTrackNames(reordered)
	removed := reorderedMeterScore(t, "removed", [2]string{"pad", "bass"}, -9, -6)
	removed.trackNames = makeTrackNames(removed)
	var snapshots [101]*liveOverrides
	var bass, lead [101]uint64
	for i := range snapshots {
		bass[i], err = p.SetTrackPreview("bass", kernel.ParamMixGain, 0)
		if err != nil {
			t.Fatal(err)
		}
		lead[i], err = p.SetTrackPreview("lead", kernel.ParamMixGain, -3)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[i] = p.overrides.Load()
	}
	iteration := 0
	var cancelErr error
	retired := true
	allocations := testing.AllocsPerRun(100, func() {
		p.overrides.Store(snapshots[iteration])
		p.applyOverrides(reordered.Engine, reordered, true)
		cancelErr = p.CancelPreview(bass[iteration])
		p.applyOverrides(reordered.Engine, reordered, false)
		p.applyOverrides(removed.Engine, removed, true)
		state := snapshots[iteration]
		retired = retired && state.tracks[p.trackNames.Load().overrideTrack(0)].state.retiredVersions[kernel.ParamMixGain].Load() == bass[iteration] && state.tracks[p.trackNames.Load().overrideTrack(1)].state.retiredVersions[kernel.ParamMixGain].Load() == lead[iteration]
		iteration++
	})
	if allocations != 0 || cancelErr != nil {
		t.Fatalf("topology/cancellation allocations=%g, error=%v", allocations, cancelErr)
	}
	if !retired {
		t.Fatal("cancellation or removal did not retire its identity")
	}
}
