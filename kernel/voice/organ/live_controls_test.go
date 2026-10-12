package organ

import (
	"math"
	"testing"
)

func liveOrgan(t *testing.T, drive float32) *Instrument {
	t.Helper()
	params := DefaultParams()
	params.Drive = drive
	o, err := New(48000, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range [...]uint8{48, 60, 64, 67} {
		_ = o.NoteOn(note, 100)
	}
	for range 1024 {
		o.NextStereo()
	}
	return o
}

// Gain and rotary mix have nothing to do with the drive bypass: at any small
// positive drive, changing them must leave the saturator fully on.
func TestGainAndRotaryMixChangesLeaveTheDriveBlendAlone(t *testing.T) {
	glide := float32(1 - math.Exp(-1/(.02*48000)))
	for _, drive := range []float32{.001, .01, .019, .3} {
		for _, control := range []struct {
			slot  int
			value float32
		}{{controlGain, 1.7}, {controlRotaryMix, .1}} {
			o := liveOrgan(t, drive)
			if err := o.SetControl(control.slot, control.value, glide); err != nil {
				t.Fatal(err)
			}
			for range 6000 {
				o.NextStereo()
				if o.driveFade != 1 {
					t.Fatalf("drive %g, control %d: blend left 1 (%g)", drive, control.slot, o.driveFade)
				}
			}
		}
	}
}

// A gain-only change at low drive equals the gain-scaled output of an
// untouched voice, sample for sample (gain is the last stage).
func TestGainOnlyChangeMatchesGainScaledReference(t *testing.T) {
	for _, drive := range []float32{.001, .01, .019} {
		ref, changed := liveOrgan(t, drive), liveOrgan(t, drive)
		if err := changed.SetControl(controlGain, 1.6, 1); err != nil {
			t.Fatal(err)
		}
		scale := 1.6 / DefaultParams().Gain
		for i := range 4096 {
			rl, rr := ref.NextStereo()
			cl, cr := changed.NextStereo()
			if math.Abs(float64(cl)-float64(rl)*float64(scale)) > 1e-6 || math.Abs(float64(cr)-float64(rr)*float64(scale)) > 1e-6 {
				t.Fatalf("drive %g frame %d: %g vs scaled %g", drive, i, cl, float64(rl)*float64(scale))
			}
		}
	}
}

// Drive passing through the small-drive region in either direction, and to or
// from zero, moves the output without a step larger than the glide allows.
func TestDriveTransitionsHaveNoStep(t *testing.T) {
	glide := float32(1 - math.Exp(-1/(.02*48000)))
	var lastRef float64
	for _, c := range []struct{ from, to float32 }{{.5, 0}, {0, .5}, {.5, .001}, {.001, .5}, {.05, .01}, {.01, .05}, {.019, .021}, {.021, .019}, {0, .019}, {.019, 0}} {
		ref, moved := liveOrgan(t, c.from), liveOrgan(t, c.from)
		if err := moved.SetControl(controlDrive, c.to, glide); err != nil {
			t.Fatal(err)
		}
		now := liveOrgan(t, c.from)
		if err := now.SetControl(controlDrive, c.to, 1); err != nil {
			t.Fatal(err)
		}
		var step, immediate, slope, largest, last float64
		for i := range 24000 {
			rl, _ := ref.NextStereo()
			ml, _ := moved.NextStereo()
			nl, _ := now.NextStereo()
			if i > 0 {
				slope = math.Max(slope, math.Abs(float64(rl)-lastRef))
				largest = math.Max(largest, math.Abs(float64(ml)-last))
				if i < 16 {
					immediate = math.Max(immediate, math.Abs(float64(nl-rl)))
					step = math.Max(step, math.Abs(float64(ml-rl)))
				}
			}
			last, lastRef = float64(ml), float64(rl)
		}
		// No sample may move more than twice what the untouched voice's own
		// waveform moves: a bypass switched with a step would exceed it.
		if largest > 2*slope {
			t.Fatalf("drive %g->%g: largest sample step %g against waveform slope %g", c.from, c.to, largest, slope)
		}
		if immediate > 1e-4 && step > immediate/5 {
			t.Fatalf("drive %g->%g glide changed the first frames by %g; immediate %g", c.from, c.to, step, immediate)
		}
	}
}
