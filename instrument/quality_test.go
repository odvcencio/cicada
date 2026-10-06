package instrument_test

import (
	"math"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

// Default patches must leave real mixer headroom before the master limiter.
// This deliberately tests raw instrument output, including an eight-note chord.
func TestBuiltinPatchesHaveUnclippedDefaultHeadroom(t *testing.T) {
	patches := instrument.Patches()
	if len(patches) == 0 {
		t.Fatal("instrument library is empty")
	}
	seen := make(map[string]bool)
	for _, patch := range patches {
		if seen[patch.ID] {
			t.Fatalf("duplicate patch ID %q", patch.ID)
		}
		seen[patch.ID] = true
		t.Run(patch.ID, func(t *testing.T) {
			p := compileQualityPatch(t, patch)
			var peak, energy, lateSum float64
			const frames = 72_000 // 1.5 seconds, past the longest default attack.
			if patch.Mode == "poly" {
				voice, err := graph.NewPoly(p, 48_000)
				if err != nil {
					t.Fatal(err)
				}
				for _, note := range [...]uint8{48, 55, 60, 64, 67, 72, 76, 79} {
					voice.NoteOn(note, 127, false)
				}
				for frame := range frames {
					left, right := voice.NextStereo()
					checkQualitySample(t, left)
					checkQualitySample(t, right)
					peak = math.Max(peak, math.Max(math.Abs(float64(left)), math.Abs(float64(right))))
					energy += (float64(left)*float64(left) + float64(right)*float64(right)) / 2
					if frame >= 48_000 {
						lateSum += (float64(left) + float64(right)) / 2
					}
				}
			} else {
				voice, err := graph.NewVoice(p, 48_000)
				if err != nil {
					t.Fatal(err)
				}
				voice.NoteOn(43, 127, false)
				for frame := range frames {
					value := voice.Next()
					checkQualitySample(t, value)
					peak = math.Max(peak, math.Abs(float64(value)))
					energy += float64(value) * float64(value)
					if frame >= 48_000 {
						lateSum += float64(value)
					}
				}
			}
			rms := math.Sqrt(energy / frames)
			dc := lateSum / (frames - 48_000)
			t.Logf("raw default %s: peak %.6f (%.2f dBFS), RMS %.6f, settled DC %.6f", patch.Mode, peak, 20*math.Log10(peak), rms, dc)
			if rms < .0001 {
				t.Fatalf("default patch is effectively silent: RMS %g", rms)
			}
			if peak > .98 {
				t.Fatalf("default patch consumes full-scale mixer headroom: peak %g", peak)
			}
			if math.Abs(dc) > .001 {
				t.Fatalf("default patch retains DC offset after its attack: %g", dc)
			}
		})
	}
}

func TestBuiltinPatchesRespondToVelocity(t *testing.T) {
	for _, patch := range instrument.Patches() {
		t.Run(patch.ID, func(t *testing.T) {
			p := compileQualityPatch(t, patch)
			low, err := graph.NewVoice(p, 48_000)
			if err != nil {
				t.Fatal(err)
			}
			high, err := graph.NewVoice(p, 48_000)
			if err != nil {
				t.Fatal(err)
			}
			note := uint8(60)
			if patch.Mode == "mono" {
				note = 43
			}
			low.NoteOn(note, 32, false)
			high.NoteOn(note, 127, false)
			var lowEnergy, highEnergy float64
			for range 48_000 {
				l, h := low.Next(), high.Next()
				checkQualitySample(t, l)
				checkQualitySample(t, h)
				lowEnergy += float64(l) * float64(l)
				highEnergy += float64(h) * float64(h)
			}
			lowRMS, highRMS := math.Sqrt(lowEnergy/48_000), math.Sqrt(highEnergy/48_000)
			t.Logf("velocity 32/127 RMS %.6f/%.6f, dynamic response %.2f dB", lowRMS, highRMS, 20*math.Log10(highRMS/lowRMS))
			if lowRMS < .00001 || highRMS < lowRMS*1.05 {
				t.Fatalf("patch does not respond to velocity: RMS at 32=%g, at 127=%g", lowRMS, highRMS)
			}
		})
	}
}

func compileQualityPatch(t testing.TB, patch instrument.Patch) graph.Program {
	t.Helper()
	source, err := patch.Source("quality_voice")
	if err != nil {
		t.Fatal(err)
	}
	scoreSource := "cicada 2\n" + source + "track quality quality_voice {}\npattern phrase notes steps=1 { 1 }\nscene main { quality=phrase }\nsong { main }\n"
	score, diagnostics := notation.Parse([]byte(scoreSource))
	if len(diagnostics) != 0 || score == nil || len(score.Instruments) != 1 {
		t.Fatalf("patch source did not parse as one authored instrument: %+v", diagnostics)
	}
	program, diagnostics := instrument.Compile(score.Instruments[0])
	if len(diagnostics) != 0 || program == nil {
		t.Fatalf("patch source did not compile: %+v", diagnostics)
	}
	if program.Mode != patch.Mode {
		t.Fatalf("library mode %q disagrees with authored mode %q", patch.Mode, program.Mode)
	}
	lowered, err := instrument.Lower(program, nil)
	if err != nil {
		t.Fatal(err)
	}
	return lowered
}

func checkQualitySample(t *testing.T, sample float32) {
	t.Helper()
	if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
		t.Fatalf("non-finite raw instrument output: %g", sample)
	}
}

var qualityPatchBenchmarkSink float32

func BenchmarkBuiltinPatchEightVoiceChord(b *testing.B) {
	for _, patch := range instrument.Patches() {
		if patch.Mode != "poly" {
			continue
		}
		b.Run(patch.ID, func(b *testing.B) {
			voice, err := graph.NewPoly(compileQualityPatch(b, patch), 48_000)
			if err != nil {
				b.Fatal(err)
			}
			for _, note := range [...]uint8{48, 55, 60, 64, 67, 72, 76, 79} {
				voice.NoteOn(note, 100, false)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				left, right := voice.NextStereo()
				qualityPatchBenchmarkSink = left + right
			}
		})
	}
}
