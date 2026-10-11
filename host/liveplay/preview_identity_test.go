package liveplay

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
)

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
		retired = retired && state.tracks["bass"].state.retiredVersions[kernel.ParamMixGain].Load() == bass[iteration] && state.tracks["lead"].state.retiredVersions[kernel.ParamMixGain].Load() == lead[iteration]
		iteration++
	})
	if allocations != 0 || cancelErr != nil {
		t.Fatalf("topology/cancellation allocations=%g, error=%v", allocations, cancelErr)
	}
	if !retired {
		t.Fatal("cancellation or removal did not retire its identity")
	}
}
