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
