package mix

import (
	"math"
	"testing"
)

func TestMusicAndSFXBusRouting(t *testing.T) {
	var bus Dry
	track := Track{Left: .5, Right: .25}
	bus.Add(1, 1, track)
	bus.AddSFX(2, 2, track)
	bus.AddReturn(.25, .5)
	musicL, musicR := bus.Music()
	sfxL, sfxR := bus.SFX()
	wantMusic := .75 * .7079457843841379
	if math.Abs(float64(musicL)-wantMusic) > 1e-7 || math.Abs(float64(musicR)-wantMusic) > 1e-7 || sfxL != 1 || sfxR != .5 {
		t.Fatalf("bus taps changed: music=(%g,%g) sfx=(%g,%g)", musicL, musicR, sfxL, sfxR)
	}
}
