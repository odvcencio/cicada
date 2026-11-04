package acid

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
)

func TestAcidExpressionRetunesWithoutRetriggerAndAllocatesNothing(t *testing.T) {
	v, err := New(48000)
	if err != nil {
		t.Fatal(err)
	}
	v.NoteOn(69, false, false, 127)
	for range 128 {
		v.Next()
	}
	originalDelta, originalPhase, originalEnvelope := v.pitchDelta, v.phaseA, v.meg
	v.NoteExpression(1200, .5, .75)
	if v.phaseA != originalPhase || v.meg != originalEnvelope {
		t.Fatal("expression retriggered acid voice")
	}
	v.Next()
	if math.Abs(v.pitchDelta/originalDelta-2) > 1e-6 {
		t.Fatal("one octave expression did not double frequency")
	}
	params := expression.Params{Set: true, PitchCents: 100, Timbre: .5, VibratoDepthCents: 30, VibratoRateHz: 5}
	v.SetExpression(params)
	if count := testing.AllocsPerRun(100, func() { v.Next() }); count != 0 {
		t.Fatalf("acid expression allocated %v", count)
	}
	v.NoteOn(69, false, true, 127)
	if v.expression.PitchCents != 0 || v.expression.VibratoDepthCents != 0 {
		t.Fatal("acid expression leaked to the next note")
	}
}

func TestAcidInstrumentVibratoChangesPitchWithoutAllocating(t *testing.T) {
	plain, err := New(48000)
	if err != nil {
		t.Fatal(err)
	}
	modulated, err := New(48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := modulated.ConfigureVibrato(expression.VibratoConfig{DepthCents: 100, RateHz: 5}); err != nil {
		t.Fatal(err)
	}
	plain.NoteOn(69, false, false, 127)
	modulated.NoteOn(69, false, false, 127)
	for range 2401 {
		plain.Next()
		modulated.Next()
	}
	if modulated.pitchDelta < plain.pitchDelta*1.05 {
		t.Fatalf("baseline vibrato did not retune acid: %g vs %g", modulated.pitchDelta, plain.pitchDelta)
	}
	if allocations := testing.AllocsPerRun(100, func() { modulated.Next() }); allocations != 0 {
		t.Fatalf("vibrato allocated %g times", allocations)
	}
}
