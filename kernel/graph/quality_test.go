package graph_test

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
)

// These checks measure bounded DSP behavior, rather than comparing subjective
// timbre with a named commercial instrument. The spectral check deliberately
// includes a naive oscillator so a broken measurement cannot pass silently.
func TestBandLimitedOscillatorsRejectFoldedHarmonics(t *testing.T) {
	const frames = 8192
	const fundamentalBin = 1173 // About 6.9 kHz at 48 kHz; three valid harmonics.
	for _, sampleRate := range []int{44_100, 48_000, 96_000} {
		for _, waveform := range []graph.Op{graph.Saw, graph.Pulse} {
			t.Run(fmt.Sprintf("%d/%d", sampleRate, waveform), func(t *testing.T) {
				frequency := float32(float64(sampleRate) * fundamentalBin / frames)
				voice, err := graph.NewVoice(qualityOscillatorProgram(waveform, frequency), sampleRate)
				if err != nil {
					t.Fatal(err)
				}
				voice.NoteOn(69, 127, false)
				for range frames { // Discard envelope and decimator startup.
					voice.Next()
				}
				actual := make([]float64, frames)
				naive := make([]float64, frames)
				for i := range actual {
					actual[i] = float64(voice.Next())
					phase := float64((i*fundamentalBin)%frames) / frames
					if waveform == graph.Pulse {
						naive[i] = -1
						if phase < .23 {
							naive[i] = 1
						}
					} else {
						naive[i] = 2*phase - 1
					}
				}
				folded, rms := harmonicResidual(actual, fundamentalBin)
				control, _ := harmonicResidual(naive, fundamentalBin)
				t.Logf("folded energy: %.6f%%, naive %.3f%%, RMS %.4f", folded*100, control*100, rms)
				if rms < .1 {
					t.Fatalf("oscillator was attenuated to silence: RMS %g", rms)
				}
				if control < .08 {
					t.Fatalf("naive negative control did not expose aliasing: residual %g", control)
				}
				if folded > .005 || folded > control*.05 {
					t.Fatalf("folded energy %.5f exceeds 0.5%% or is not at least 20x lower than naive %.5f", folded, control)
				}
			})
		}
	}
}

// A coherent window has no leakage: subtract DC and every valid harmonic's
// Fourier energy from total energy. What remains is folded harmonics, noise,
// or another unintended frequency. No windowing can hide a naive saw's aliases.
func harmonicResidual(samples []float64, fundamentalBin int) (fraction, rms float64) {
	n := float64(len(samples))
	var sum, energy float64
	for _, sample := range samples {
		sum += sample
		energy += sample * sample
	}
	total := energy / n
	valid := (sum / n) * (sum / n)
	for bin := fundamentalBin; bin < len(samples)/2; bin += fundamentalBin {
		var real, imag float64
		for i, sample := range samples {
			phase := 2 * math.Pi * float64(i*bin) / n
			real += sample * math.Cos(phase)
			imag -= sample * math.Sin(phase)
		}
		valid += 2 * (real*real + imag*imag) / (n * n)
	}
	if total == 0 {
		return 0, 0
	}
	return math.Max(0, (total-valid)/total), math.Sqrt(total)
}

func qualityOscillatorProgram(waveform graph.Op, frequency float32) graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Constant, Value: frequency}
	p.Nodes[1] = graph.Node{Op: graph.Gate}
	p.Nodes[2] = graph.Node{Op: graph.Constant, Value: 1} // Attack milliseconds.
	p.Nodes[3] = graph.Node{Op: graph.Constant, Value: 1} // Decay milliseconds.
	p.Nodes[4] = graph.Node{Op: graph.Constant, Value: 1} // Sustain level.
	p.Nodes[5] = graph.Node{Op: graph.Constant, Value: 5} // Release milliseconds.
	p.Nodes[6] = graph.Node{Op: graph.Constant, Value: .23}
	p.Nodes[7] = graph.Node{Op: waveform, A: 0, B: 6}
	p.Nodes[8] = graph.Node{Op: graph.ADSR, A: 1, B: 2, C: 3, D: 4, E: 5}
	p.Nodes[9] = graph.Node{Op: graph.Multiply, A: 7, B: 8}
	p.Len, p.Output = 10, 9
	return p
}

func TestADSRHeldSustainAndAuthoredRelease(t *testing.T) {
	for _, sampleRate := range []int{44_100, 48_000, 96_000} {
		t.Run(fmt.Sprint(sampleRate), func(t *testing.T) {
			p := qualitySineProgram()
			p.Nodes[3].Value, p.Nodes[4].Value = 10, 20
			p.Nodes[5].Value, p.Nodes[6].Value = .35, 40
			p.Output = 8 // Observe the envelope independently of oscillator phase.
			voice, err := graph.NewVoice(p, sampleRate)
			if err != nil {
				t.Fatal(err)
			}
			for range 128 {
				if got := voice.Next(); got != 0 {
					t.Fatalf("ungated envelope was audible: %g", got)
				}
			}
			voice.NoteOn(60, 127, false)
			var peak float32
			for frame := range sampleRate*40/1000 + 64 {
				value := voice.Next()
				peak = max(peak, value)
				if frame == sampleRate*3/1000+24 && (value <= .001 || value >= .98) {
					t.Fatalf("authored 10 ms attack did not rise gradually: 3 ms level %g", value)
				}
				if frame == sampleRate*22/1000+24 && (value <= .35001 || value >= .98) {
					t.Fatalf("authored 20 ms decay did not approach sustain gradually: level %g", value)
				}
			}
			if peak < .9 {
				t.Fatalf("attack never reached its peak before decay: %g", peak)
			}
			for range 128 {
				if got := voice.Next(); math.Abs(float64(got)-.35) > 1e-5 {
					t.Fatalf("held envelope did not sustain at 0.35: %g", got)
				}
			}
			voice.NoteOff()
			for range sampleRate * 20 / 1000 {
				voice.Next()
			}
			mid := voice.Next()
			if mid <= 0 || mid >= .35 {
				t.Fatalf("release did not continue from sustain: midpoint %g", mid)
			}
			for range sampleRate * 16 / 1000 {
				voice.Next()
			}
			if got := voice.Next(); got <= 0 {
				t.Fatalf("release ended before its authored 40 ms duration: 36 ms level %g", got)
			}
			for range sampleRate*9/1000 + 64 {
				voice.Next()
			}
			for range 128 {
				if got := voice.Next(); math.Abs(float64(got)) > 1e-7 {
					t.Fatalf("release exceeded authored 40 ms plus decimator latency: %g", got)
				}
			}
		})
	}
}

func TestStateVariableFilterIsFiniteAtRateAndParameterExtremes(t *testing.T) {
	for _, sampleRate := range []int{44_100, 48_000, 96_000} {
		for _, cutoff := range []float32{-100, 0, 20, 1000, float32(sampleRate) * .45, float32(sampleRate), 1e9} {
			for _, resonance := range []float32{-1, 0, .5, .99, 1, 2} {
				t.Run(fmt.Sprintf("%d/cutoff=%g/res=%g", sampleRate, cutoff, resonance), func(t *testing.T) {
					p := qualitySineProgram()
					p.Nodes[7].Op = graph.Pulse
					p.Nodes[7].B = 5 // Sustain is 0.7, also a valid pulse width.
					p.Nodes[11] = graph.Node{Op: graph.Constant, Value: cutoff}
					p.Nodes[12] = graph.Node{Op: graph.Constant, Value: resonance}
					p.Nodes[13] = graph.Node{Op: graph.SVF, A: 7, B: 11, C: 12}
					p.Nodes[14] = graph.Node{Op: graph.Multiply, A: 13, B: 8}
					p.Len, p.Output = 15, 14
					voice, err := graph.NewVoice(p, sampleRate)
					if err != nil {
						t.Fatal(err)
					}
					voice.NoteOn(96, 127, false)
					var peak float64
					for range sampleRate / 10 {
						value := float64(voice.Next())
						if math.IsNaN(value) || math.IsInf(value, 0) {
							t.Fatalf("non-finite sample %g", value)
						}
						peak = math.Max(peak, math.Abs(value))
					}
					if peak > 16 {
						t.Fatalf("filter has runaway gain: peak %g for unit oscillator", peak)
					}
					if cutoff == 1000 && resonance == .5 && peak < .001 {
						t.Fatalf("normal filter settings produced silence: peak %g", peak)
					}
					voice.NoteOff()
					for range sampleRate/10 + 64 {
						voice.Next()
					}
					for range 128 {
						if got := voice.Next(); got != 0 {
							t.Fatalf("released filter voice did not become silent: %g", got)
						}
					}
				})
			}
		}
	}
}

func TestDerivedNonfiniteOscillatorFrequencyDoesNotPoisonOtherSignal(t *testing.T) {
	for _, waveform := range []graph.Op{graph.Saw, graph.Pulse, graph.Sine} {
		for _, nonfiniteNode := range []uint8{3, 4} {
			t.Run(fmt.Sprintf("wave=%d/source=%d", waveform, nonfiniteNode), func(t *testing.T) {
				var p graph.Program
				p.Nodes[0] = graph.Node{Op: graph.Pitch}
				p.Nodes[1] = graph.Node{Op: graph.Constant, Value: math.MaxFloat32}
				p.Nodes[2] = graph.Node{Op: graph.Constant, Value: math.MaxFloat32}
				p.Nodes[3] = graph.Node{Op: graph.Multiply, A: 1, B: 2} // Infinity from finite authored values.
				p.Nodes[4] = graph.Node{Op: graph.Subtract, A: 3, B: 3} // NaN from that derived value.
				p.Nodes[5] = graph.Node{Op: graph.Constant, Value: .3}
				p.Nodes[6] = graph.Node{Op: waveform, A: nonfiniteNode, B: 5}
				p.Nodes[7] = graph.Node{Op: graph.Sine, A: 0}
				p.Nodes[8] = graph.Node{Op: graph.Add, A: 6, B: 7}
				p.Nodes[9] = graph.Node{Op: graph.Gate}
				p.Nodes[10] = graph.Node{Op: graph.Constant, Value: 1}
				p.Nodes[11] = graph.Node{Op: graph.Constant, Value: 1}
				p.Nodes[12] = graph.Node{Op: graph.Constant, Value: 1}
				p.Nodes[13] = graph.Node{Op: graph.Constant, Value: 1}
				p.Nodes[14] = graph.Node{Op: graph.ADSR, A: 9, B: 10, C: 11, D: 12, E: 13}
				p.Nodes[15] = graph.Node{Op: graph.Multiply, A: 8, B: 14}
				p.Len, p.Output = 16, 15
				voice, err := graph.NewVoice(p, 48_000)
				if err != nil {
					t.Fatal(err)
				}
				voice.NoteOn(69, 127, false)
				for range 256 {
					voice.Next()
				}
				var energy float64
				for range 2048 {
					value := float64(voice.Next())
					if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 2.1 {
						t.Fatalf("derived nonfinite frequency escaped oscillator boundary: %g", value)
					}
					energy += value * value
				}
				if rms := math.Sqrt(energy / 2048); rms < .1 {
					t.Fatalf("bad oscillator poisoned the independent valid oscillator: RMS %g", rms)
				}
			})
		}
	}
}

func qualitySineProgram() graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Pitch}
	p.Nodes[1] = graph.Node{Op: graph.Gate}
	p.Nodes[2] = graph.Node{Op: graph.Velocity}
	p.Nodes[3] = graph.Node{Op: graph.Constant, Value: 2}
	p.Nodes[4] = graph.Node{Op: graph.Constant, Value: 5}
	p.Nodes[5] = graph.Node{Op: graph.Constant, Value: .7}
	p.Nodes[6] = graph.Node{Op: graph.Constant, Value: 20}
	p.Nodes[7] = graph.Node{Op: graph.Sine, A: 0}
	p.Nodes[8] = graph.Node{Op: graph.ADSR, A: 1, B: 3, C: 4, D: 5, E: 6}
	p.Nodes[9] = graph.Node{Op: graph.Multiply, A: 7, B: 8}
	p.Nodes[10] = graph.Node{Op: graph.Multiply, A: 9, B: 2}
	p.Len, p.Output = 11, 10
	return p
}

func TestPolyphonicNoteReleaseLeavesOtherNoteHeld(t *testing.T) {
	const sampleRate = 48_000
	poly, err := graph.NewPoly(qualitySineProgram(), sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	poly.NoteOn(60, 127, false)
	poly.NoteOn(72, 127, false)
	for range sampleRate / 10 {
		poly.NextStereo()
	}
	poly.NoteOffNote(60)
	for range sampleRate / 20 { // Past the 20 ms release, including FIR latency.
		poly.NextStereo()
	}
	var energy float64
	samples := make([]float64, sampleRate/10)
	for i := range samples {
		left, right := poly.NextStereo()
		energy += float64(left)*float64(left) + float64(right)*float64(right)
		samples[i] = (float64(left) + float64(right)) / 2
	}
	if rms := math.Sqrt(energy / (2 * (sampleRate / 10))); rms < .01 {
		t.Fatalf("releasing one note silenced the held note: RMS %g", rms)
	}
	released := qualityToneAmplitude(samples, 440*math.Exp2((60.0-69)/12), sampleRate)
	held := qualityToneAmplitude(samples, 440*math.Exp2((72.0-69)/12), sampleRate)
	if held < .01 || released > held*.01 {
		t.Fatalf("wrong note remained after release: released-note amplitude %g, held-note amplitude %g", released, held)
	}
	poly.NoteOffNote(72)
	for range sampleRate/20 + 64 {
		poly.NextStereo()
	}
	for range 128 {
		left, right := poly.NextStereo()
		if math.Abs(float64(left))+math.Abs(float64(right)) > 1e-7 {
			t.Fatalf("all released notes did not become silent: (%g, %g)", left, right)
		}
	}
}

func TestPolyphonicPoolKeepsEightDistinctPitchesAudible(t *testing.T) {
	const sampleRate = 48_000
	voice, err := graph.NewPoly(qualitySineProgram(), sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range qualityChord {
		voice.NoteOn(note, 127, false)
	}
	for range 4800 {
		voice.NextStereo()
	}
	samples := make([]float64, 12_000)
	for i := range samples {
		left, right := voice.NextStereo()
		samples[i] = (float64(left) + float64(right)) / 2
	}
	var amplitudes [len(qualityChord)]float64
	var strongest float64
	for i, note := range qualityChord {
		frequency := 440 * math.Exp2((float64(note)-69)/12)
		amplitudes[i] = qualityToneAmplitude(samples, frequency, sampleRate)
		strongest = math.Max(strongest, amplitudes[i])
	}
	if strongest < .01 {
		t.Fatal("eight-note chord remained silent")
	}
	for i, amplitude := range amplitudes {
		if amplitude < strongest*.8 {
			t.Fatalf("note %d disappeared from eight-note chord: amplitude %g, strongest %g", qualityChord[i], amplitude, strongest)
		}
	}
}

func qualityToneAmplitude(samples []float64, frequency float64, sampleRate int) float64 {
	var real, imag, weights float64
	for i, sample := range samples {
		weight := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(len(samples)-1))
		phase := 2 * math.Pi * frequency * float64(i) / float64(sampleRate)
		real += sample * weight * math.Cos(phase)
		imag -= sample * weight * math.Sin(phase)
		weights += weight
	}
	return 2 * math.Hypot(real, imag) / weights
}

func TestPolyphonicReleasePreservesHeldGainAndPairsRepeatedPitches(t *testing.T) {
	for _, repeatedPitch := range []bool{false, true} {
		t.Run(fmt.Sprintf("repeated=%t", repeatedPitch), func(t *testing.T) {
			p := qualitySineProgram()
			voice, err := graph.NewPoly(p, 48_000)
			if err != nil {
				t.Fatal(err)
			}
			reference, err := graph.NewPoly(p, 48_000)
			if err != nil {
				t.Fatal(err)
			}
			secondNote := uint8(72)
			if repeatedPitch {
				secondNote = 60
			}
			voice.NoteOn(60, 127, false)
			reference.NoteOn(60, 0, false) // Same slot/history, inaudible first voice.
			voice.NoteOn(secondNote, 100, false)
			reference.NoteOn(secondNote, 100, false)
			for range 4800 {
				voice.NextStereo()
				reference.NextStereo()
			}
			voice.NoteOffNote(60)
			reference.NoteOffNote(60)
			for range 2400 {
				voice.NextStereo()
				reference.NextStereo()
			}
			var peak float32
			for range 4800 {
				left, right := voice.NextStereo()
				wantLeft, wantRight := reference.NextStereo()
				peak = max(peak, absQuality(left), absQuality(right))
				if math.Abs(float64(left-wantLeft))+math.Abs(float64(right-wantRight)) > 1e-6 {
					t.Fatalf("releasing another voice changed the held note's gain/phase: (%g,%g), reference (%g,%g)", left, right, wantLeft, wantRight)
				}
			}
			if peak < .01 {
				t.Fatal("the second instance was released with the first")
			}
			voice.NoteOffNote(secondNote)
			for range 2400 {
				voice.NextStereo()
			}
			for range 128 {
				left, right := voice.NextStereo()
				if left != 0 || right != 0 {
					t.Fatalf("second note-off left a repeated pitch stuck: (%g,%g)", left, right)
				}
			}
		})
	}
}

func TestADSREnvelopeRetriggersDuringReleaseWithoutJump(t *testing.T) {
	p := qualitySineProgram()
	p.Output = 8
	voice, err := graph.NewVoice(p, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	voice.NoteOn(60, 127, false)
	for range 2400 {
		voice.Next()
	}
	voice.NoteOff()
	var previous float32
	for range 480 {
		previous = voice.Next()
	}
	if previous <= 0 || previous >= .7 {
		t.Fatalf("fixture was not in release at retrigger: %g", previous)
	}
	voice.NoteOn(60, 127, false)
	if got := voice.Next(); math.Abs(float64(got-previous)) > .01 {
		t.Fatalf("retrigger produced an envelope discontinuity: %g to %g", previous, got)
	}
	for range 2400 {
		voice.Next()
	}
	if got := voice.Next(); math.Abs(float64(got)-.7) > 1e-5 {
		t.Fatalf("retrigger never returned to held sustain: %g", got)
	}
	voice.NoteOff()
	for range 2400 {
		voice.Next()
	}
	if got := voice.Next(); got != 0 {
		t.Fatalf("retrigger left an envelope stuck after final release: %g", got)
	}
}

func TestNinthVoiceStealDeclicksAndReturnsToFixedGain(t *testing.T) {
	p := qualitySineProgram()
	p.Output = 8 // A held envelope makes any steal discontinuity unambiguous.
	voice, err := graph.NewPoly(p, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range qualityChord {
		voice.NoteOn(note, 127, false)
	}
	for range 4800 {
		voice.NextStereo()
	}
	before, _ := voice.NextStereo()
	voice.NoteOn(84, 127, false)
	after, _ := voice.NextStereo()
	if math.Abs(float64(after-before)) > .01 {
		t.Fatalf("ninth-note steal removed the old voice abruptly: %g to %g", before, after)
	}
	for range 2400 {
		voice.NextStereo()
	}
	settled, _ := voice.NextStereo()
	if math.Abs(float64(settled-before)) > 1e-5 {
		t.Fatalf("voice stealing changed held chord gain or left an extra tail: before %g, settled %g", before, settled)
	}
	voice.NoteOff()
	for range 2400 {
		voice.NextStereo()
	}
	left, right := voice.NextStereo()
	if left != 0 || right != 0 {
		t.Fatalf("stolen/released chord did not become silent: (%g,%g)", left, right)
	}
}

func absQuality(value float32) float32 {
	return float32(math.Abs(float64(value)))
}

func TestPolyphonicStealingIsDeterministicAndResetIsSilent(t *testing.T) {
	first, err := graph.NewPoly(qualitySineProgram(), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	second, err := graph.NewPoly(qualitySineProgram(), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for frame := range 16_000 {
		if frame%503 == 0 {
			note := uint8(48 + (frame/503)%24)
			first.NoteOn(note, 96, false)
			second.NoteOn(note, 96, false)
		}
		if frame%701 == 0 {
			note := uint8(48 + (frame/701)%24)
			first.NoteOffNote(note)
			second.NoteOffNote(note)
		}
		left, right := first.NextStereo()
		otherLeft, otherRight := second.NextStereo()
		if left != otherLeft || right != otherRight {
			t.Fatalf("same event history diverged at sample %d: (%g,%g) != (%g,%g)", frame, left, right, otherLeft, otherRight)
		}
		if math.IsNaN(float64(left)) || math.IsInf(float64(left), 0) || math.IsNaN(float64(right)) || math.IsInf(float64(right), 0) {
			t.Fatalf("non-finite stolen-voice output at sample %d", frame)
		}
	}
	first.Reset()
	for range 128 {
		left, right := first.NextStereo()
		if left != 0 || right != 0 {
			t.Fatalf("Reset retained voice or declick tail: (%g,%g)", left, right)
		}
	}
}

func TestHighQualityVoiceAndEightVoiceChordAllocateNothingInCallback(t *testing.T) {
	p := qualitySineProgram()
	voice, err := graph.NewVoice(p, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	poly, err := graph.NewPoly(p, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range qualityChord {
		poly.NoteOn(note, 100, false)
	}
	voice.NoteOn(60, 100, false)
	if got := testing.AllocsPerRun(100, func() {
		for range 128 {
			voice.Next()
			poly.NextStereo()
		}
	}); got != 0 {
		t.Fatalf("voice/chord render allocated %.1f objects", got)
	}
	var event int
	if got := testing.AllocsPerRun(100, func() {
		note := uint8(36 + event%60)
		event++
		poly.NoteOn(note, 100, false) // Forces stealing at the fixed voice budget.
		poly.NextStereo()
		poly.NoteOffNote(note)
		poly.NextStereo()
		poly.NoteOff()
		voice.NoteOn(72, 100, false)
		voice.NoteOff()
	}); got != 0 {
		t.Fatalf("note/release/stealing callback allocated %.1f objects", got)
	}
}

var qualityChord = [...]uint8{48, 55, 60, 64, 67, 72, 76, 79}

var qualityBenchmarkSink float32

func BenchmarkEightVoiceHighQualityChord(b *testing.B) {
	poly, err := graph.NewPoly(qualitySineProgram(), 48_000)
	if err != nil {
		b.Fatal(err)
	}
	for _, note := range qualityChord {
		poly.NoteOn(note, 100, false)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		left, right := poly.NextStereo()
		qualityBenchmarkSink = left + right
	}
}
