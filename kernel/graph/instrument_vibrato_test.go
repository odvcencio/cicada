package graph

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
)

func TestInstrumentVibratoAffectsPitchAfterDelay(t *testing.T) {
	program := sineProgram(60)
	program.Vibrato = &expression.VibratoConfig{DepthCents: 100, RateHz: 5, DelayMS: 10}
	voice, err := NewVoice(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(69, 127, false)
	for range 481 {
		voice.Next()
	}
	if got := voice.values[0]; math.Abs(float64(got)-440) > .0001 {
		t.Fatalf("vibrato sounded during delay: %g", got)
	}
	for range 2400 {
		voice.Next()
	}
	if got := voice.values[0]; got < 465 || got > 467 {
		t.Fatalf("vibrato quarter-cycle pitch = %g", got)
	}
	voice.NoteOn(69, 127, false)
	for range 480 {
		voice.Next()
	}
	if got := voice.values[0]; math.Abs(float64(got)-440) > .0001 {
		t.Fatalf("new note did not restart vibrato delay: %g", got)
	}
	if allocations := testing.AllocsPerRun(100, func() { voice.Next() }); allocations != 0 {
		t.Fatalf("vibrato allocated %g times", allocations)
	}
}
