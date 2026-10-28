package drum

import (
	"testing"

	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

func TestMappedModeledHatsChokeByArticulation(t *testing.T) {
	k, _ := New(48000, 7)
	for lane := Lane(0); lane < LaneCount; lane++ {
		k.Disable(lane)
	}
	// Deliberately use source lanes unrelated to the legacy CH/OH pair.
	for lane, profile := range map[Lane]modeledkit.Profile{BD: modeledkit.HatOpen, CY: modeledkit.HatPedal, OH: modeledkit.Snare} {
		if err := k.SetModeled(lane, profile, modeledkit.DefaultParams(), -6, 0); err != nil {
			t.Fatal(err)
		}
	}
	k.Hit(BD, 100, false)
	k.Hit(OH, 100, false)
	for i := 0; i < 480; i++ {
		k.NextStereo()
	}
	k.Hit(CY, 100, false)
	for i := 0; i < 240; i++ {
		k.NextStereo()
	}
	if k.lanes[BD].modeled.Active() {
		t.Fatal("pedal did not choke mapped open hat")
	}
	if !k.lanes[OH].modeled.Active() {
		t.Fatal("hat choke silenced unrelated snare")
	}
	if err := k.SetRecipe(BD, BD); err != nil {
		t.Fatal(err)
	}
	if k.lanes[BD].modeled != nil {
		t.Fatal("recipe retained prior model")
	}
}

func TestModeledChokeResetAndLiveMix(t *testing.T) {
	k, _ := New(48000, 42)
	for lane := Lane(0); lane < LaneCount; lane++ {
		k.Disable(lane)
	}
	if err := k.SetModeled(CY, modeledkit.Crash, modeledkit.DefaultParams(), -6, 0); err != nil {
		t.Fatal(err)
	}
	k.Hit(CY, 127, false)
	for i := 0; i < 4800; i++ {
		k.NextStereo()
	}
	p := k.Params(CY)
	p.LevelDB, p.Pan = -12, -.3
	if err := k.SetParamsTarget(CY, p, .1); err != nil {
		t.Fatal(err)
	}
	k.NoteOff(CY)
	for i := 0; i < 240; i++ {
		k.NextStereo()
	}
	if k.lanes[CY].modeled.Active() {
		t.Fatal("cymbal choke exceeded 5ms")
	}
	p.Tune *= 1.1
	if err := k.SetParamsTarget(CY, p, .1); err == nil {
		t.Fatal("accepted unimplemented live synthesis control")
	}
	k.Reset()
	for i := 0; i < 128; i++ {
		l, r := k.NextStereo()
		if l != 0 || r != 0 {
			t.Fatal("reset is not silent")
		}
	}
}

func TestModeledBrushSweepHoldsUntilItsLaneIsReleased(t *testing.T) {
	k, _ := New(48000, 11)
	for lane := Lane(0); lane < LaneCount; lane++ {
		k.Disable(lane)
	}
	for lane, profile := range map[Lane]modeledkit.Profile{CP: modeledkit.BrushSweep, SD: modeledkit.BrushTap, OH: modeledkit.HatOpen, CH: modeledkit.HatClosed} {
		if err := k.SetModeled(lane, profile, modeledkit.DefaultParams(), -6, 0); err != nil {
			t.Fatal(err)
		}
	}
	k.Hit(CP, 100, false)
	k.Hit(OH, 100, false)
	for i := 0; i < 24_000; i++ {
		k.NextStereo()
	}
	if !k.lanes[CP].modeled.Active() {
		t.Fatal("a sweep did not hold for half a second")
	}
	// Hat choking follows the hat family: a closed hat damps the open hat,
	// not a brush on any lane.
	k.Hit(CH, 100, false)
	k.Hit(SD, 100, false)
	for i := 0; i < 480; i++ {
		k.NextStereo()
	}
	if k.lanes[OH].modeled.Active() || !k.lanes[CP].modeled.Active() {
		t.Fatal("hat choke missed a hat or silenced a sweep")
	}
	k.NoteOff(CP)
	for i := 0; i < 240; i++ {
		k.NextStereo()
	}
	if k.lanes[CP].modeled.Active() {
		t.Fatal("releasing the lane left the sweep sounding past the 5 ms choke")
	}
	// A score row has no gate end, so an unreleased sweep holds for 1.6 s at
	// Decay 1, fades, and has ended by 2.5 s.
	k.Hit(CP, 100, false)
	for i := 0; i < 72_000; i++ {
		k.NextStereo()
	}
	if !k.lanes[CP].modeled.Active() {
		t.Fatal("an ungated sweep ended inside its hold")
	}
	for i := 0; i < 48_000; i++ {
		k.NextStereo()
	}
	if k.lanes[CP].modeled.Active() {
		t.Fatal("an ungated sweep never ended")
	}
}

func TestBuiltinHatOnCHStillChokesWhateverSoundsOnOH(t *testing.T) {
	// A modeled hat leaves a brush alone, but a built-in closed hat chokes the
	// open-hat lane by name, so it also stops a sweep bound to that lane.
	k, _ := New(48000, 11)
	for lane := Lane(0); lane < LaneCount; lane++ {
		k.Disable(lane)
	}
	if err := k.SetRecipe(CH, CH); err != nil {
		t.Fatal(err)
	}
	if err := k.SetModeled(OH, modeledkit.BrushSweep, modeledkit.DefaultParams(), -6, 0); err != nil {
		t.Fatal(err)
	}
	k.Hit(OH, 100, false)
	for i := 0; i < 24_000; i++ {
		k.NextStereo()
	}
	if !k.lanes[OH].modeled.Active() {
		t.Fatal("a sweep did not hold for half a second")
	}
	k.Hit(CH, 100, false)
	for i := 0; i < 480; i++ {
		k.NextStereo()
	}
	if k.lanes[OH].modeled.Active() {
		t.Fatal("a built-in closed hat left the sweep on the open-hat lane sounding")
	}
}
