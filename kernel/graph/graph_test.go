package graph

import (
	"math"
	"testing"
)

func TestVoiceRunsAllocationFree(t *testing.T) {
	var program Program
	program.Nodes[0] = Node{Op: Pitch}
	program.Nodes[1] = Node{Op: Sine, A: 0}
	program.Len = 2
	program.Output = 1
	program.GlideMS = 60
	voice, err := NewVoice(program, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(69, 127, false)
	voice.NoteOn(72, 127, true)
	allocs := testing.AllocsPerRun(100, func() { voice.Next() })
	if allocs != 0 {
		t.Fatalf("audio callback allocated %.1f objects", allocs)
	}
	var nonzero bool
	for range 128 {
		if voice.Next() != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		t.Fatal("oscillator remained silent")
	}
}

func TestVoiceSlideFollowsAcidExponentialPitchTrajectory(t *testing.T) {
	const sampleRate = 48_000
	voice, err := NewVoice(sineProgram(60), sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(60, 127, false)
	for range 1_043 {
		voice.Next()
	}
	voice.NoteOn(72, 127, true)

	const durationMS = 130
	samples := make([]float32, sampleRate*durationMS/1000)
	for i := range samples {
		samples[i] = voice.Next()
	}
	if math.Abs(float64(samples[0])) < 0.1 {
		t.Fatalf("slide reset oscillator phase: first sample %.6f", samples[0])
	}
	crossings := positiveZeroCrossings(samples)
	if len(crossings) < 2 {
		t.Fatal("sine voice produced too few positive zero crossings")
	}

	startLog := math.Log2(440) + (60.0-69)/12
	targetLog := math.Log2(440) + (72.0-69)/12
	for _, atMS := range [...]int{0, 10, 30, 60, 120} {
		atFrame := float64(atMS * sampleRate / 1000)
		var measured, midpoint float64
		bestDistance := math.Inf(1)
		for i := 1; i < len(crossings); i++ {
			left, right := crossings[i-1], crossings[i]
			periodMidpoint := (left + right) / 2
			distance := math.Abs(periodMidpoint - atFrame)
			if distance < bestDistance {
				bestDistance = distance
				midpoint = periodMidpoint
				measured = float64(sampleRate) / (right - left)
			}
		}
		elapsed := (midpoint + 1) / sampleRate
		want := math.Exp2(startLog + (targetLog-startLog)*(1-math.Exp(-elapsed/.060)))
		relativeError := math.Abs(measured-want) / want
		t.Logf("%d ms: measured %.4f Hz, analytic %.4f Hz, relative error %.4f%%", atMS, measured, want, relativeError*100)
		if relativeError > 0.02 {
			t.Fatalf("slide pitch at %d ms was %.4f Hz; analytic exponential is %.4f Hz (error %.3f%%, tolerance 2%%)", atMS, measured, want, relativeError*100)
		}
	}
}

func TestVoiceNonSlideJumpsAndRetriggers(t *testing.T) {
	voice, err := NewVoice(sineProgram(60), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(60, 127, false)
	for range 731 {
		voice.Next()
	}
	voice.NoteOn(72, 127, false)
	if got := voice.Next(); got != 0 {
		t.Fatalf("non-slide note did not retrigger phase: first sample %.8f", got)
	}
	got := voice.Next()
	phaseStep := float32(440 * math.Exp2((72.0-69)/12) / 48_000)
	want := float32(math.Sin(2 * math.Pi * float64(phaseStep)))
	if math.Abs(float64(got-want)) > 1e-7 {
		t.Fatalf("non-slide note did not jump to target pitch: sample %.8f, want %.8f", got, want)
	}
}

func TestZeroGlideKeepsSlidePitchImmediate(t *testing.T) {
	voice, err := NewVoice(sineProgram(0), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(60, 127, false)
	voice.Next()
	phase := voice.states[1].phase
	voice.NoteOn(72, 127, true)
	if voice.gliding || voice.pitch != float32(440*math.Exp2((72.0-69)/12)) {
		t.Fatalf("zero glide did not jump immediately: pitch=%g gliding=%v", voice.pitch, voice.gliding)
	}
	if voice.states[1].phase != phase {
		t.Fatal("zero-time slide retriggered the oscillator")
	}
}

func sineProgram(glideMS float64) Program {
	var program Program
	program.Nodes[0] = Node{Op: Pitch}
	program.Nodes[1] = Node{Op: Sine, A: 0}
	program.Len = 2
	program.Output = 1
	program.GlideMS = glideMS
	return program
}

func positiveZeroCrossings(samples []float32) []float64 {
	crossings := make([]float64, 0, len(samples)/128)
	for i := 1; i < len(samples); i++ {
		left, right := float64(samples[i-1]), float64(samples[i])
		if left <= 0 && right > 0 {
			crossings = append(crossings, float64(i-1)-left/(right-left))
		}
	}
	return crossings
}

func TestRejectsGraphCycle(t *testing.T) {
	var program Program
	program.Nodes[0] = Node{Op: Sine, A: 0}
	program.Len = 1
	if _, err := NewVoice(program, 48_000); err == nil {
		t.Fatal("self-referential graph was accepted")
	}
}
