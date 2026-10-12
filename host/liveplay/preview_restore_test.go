package liveplay

import (
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
)

func unavailablePreviewRestoreScore(t *testing.T, missing bool) Score {
	score := previewParameterScore(t, engine.VoiceDrums)
	for i, parameter := range score.Parameters {
		if parameter.ID == kernel.ParamDrumSdTune {
			if missing {
				score.Parameters = append(score.Parameters[:i], score.Parameters[i+1:]...)
			} else {
				score.Parameters[i].Value = .7 // A raw caller omitted inward conversion.
			}
			break
		}
	}
	return score
}

func TestPreviewUnavailableRestoreRetiresAndReports(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "invalid"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			p, err := New(unavailablePreviewRestoreScore(t, missing), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			version, err := p.SetPreview(0, kernel.ParamDrumSdTune, 1)
			if err != nil {
				t.Fatal(err)
			}
			renderPreview(t, p, blockFrames)
			if err := p.CancelPreview(version); err != nil {
				t.Fatalf("cancellation was not queued: %v", err)
			}
			renderPreview(t, p, blockFrames)
			if _, active := p.OverrideValue(0, kernel.ParamDrumSdTune); active || !p.current.Engine.Playing() {
				t.Fatal("unavailable restore kept an override active or stopped playback")
			}
			select {
			case event := <-p.Events():
				if event.Kind != "preview-error" || event.Name != "drum.sd.tune" || event.Track != "bass" {
					t.Fatalf("restore report: %+v", event)
				}
			default:
				t.Fatal("unavailable restoration was not reported")
			}
		})
	}
}

func TestPreviewUnavailableRestoreDoesNotAllocateOnTheRenderThread(t *testing.T) {
	for _, missing := range []bool{false, true} {
		p, err := New(unavailablePreviewRestoreScore(t, missing), 48_000)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		var snapshots [101]*liveOverrides
		var versions [101]uint64
		for i := range snapshots {
			versions[i], err = p.SetPreview(0, kernel.ParamDrumSdTune, 1)
			if err != nil {
				t.Fatal(err)
			}
			snapshots[i] = p.overrides.Load()
		}
		var output [blockFrames * 8]byte
		var readErr error
		iteration := 0
		allocations := testing.AllocsPerRun(100, func() {
			p.overrides.Store(snapshots[iteration])
			p.CancelPreviewWait(versions[iteration])
			_, readErr = p.Read(output[:])
			iteration++
		})
		if allocations != 0 || readErr != nil {
			t.Fatalf("render allocations=%g, error=%v", allocations, readErr)
		}
	}
}
