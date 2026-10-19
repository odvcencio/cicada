package expression

import (
	"math"
	"testing"
)

func TestSharedControlsKeepUnclampedMemberAndVibratoPhase(t *testing.T) {
	var state State
	state.Reset()
	member := Params{Set: true, PitchCents: 9000, Pressure: .7, Timbre: 1, VibratoDepthCents: 23, VibratoRateHz: 6.25}
	state.SetParams(member, 48000)
	for range 123 {
		state.NextCents()
	}
	phase, increment := state.phase, state.increment
	state.SetGroupControls(Controls{PitchCents: 9000, Pressure: .8, Timbre: 1})
	if state.PitchCents != 9600 || state.Pressure != .8 || state.Timbre != 1 || state.phase != phase || state.increment != increment {
		t.Fatal("shared controls changed the member oscillator", state)
	}
	state.SetGroupControls(Controls{Timbre: .5})
	if state.Params != member {
		t.Fatal("clamping destroyed the original member snapshot", state.Params)
	}
	state.SetControls(-200, .2, 0)
	state.SetGroupControls(Controls{PitchCents: 50, Pressure: .8, Timbre: .8})
	if state.PitchCents != -150 || state.Pressure != .8 || math.Abs(float64(state.Timbre-.3)) > 1e-6 || state.VibratoDepthCents != 23 {
		t.Fatal(state.Params)
	}
	if got := testing.AllocsPerRun(1000, func() { state.SetGroupControls(Controls{PitchCents: 100, Timbre: .5}); state.NextRatio() }); got != 0 {
		t.Fatal("shared control callback allocated", got)
	}
	state.Reset()
	state.SetControls(25, .3, .4)
	if state.PitchCents != 25 || state.Pressure != .3 || state.Timbre != .4 {
		t.Fatal("group leaked across a note reset", state.Params)
	}
}
