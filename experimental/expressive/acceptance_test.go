package expressive

import (
	"math"
	"testing"
)

var voiceFactories = []struct {
	name string
	make func(int) Voice
}{
	{"bow", func(sr int) Voice { return NewBow(sr) }},
	{"brass", func(sr int) Voice { return NewBrass(sr) }},
	{"guitar", func(sr int) Voice { return NewGuitar(sr) }},
}

func normalExpression(hz float64) Expression {
	return Expression{PitchHz: hz, Pressure: .65, Position: .24, Brightness: .65, Drive: .2, Damping: .1}
}

// This is a numeric containment limit, not an audio loudness specification.
func requireSafeSample(t *testing.T, x float64, sample int) {
	t.Helper()
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 16 {
		t.Fatalf("unsafe output at sample %d: %g", sample, x)
	}
}

func TestVoiceFiniteAcrossRatesAndControls(t *testing.T) {
	for _, factory := range voiceFactories {
		for _, sr := range []int{8000, 44100, 48000, 96000, 192000} {
			t.Run(factory.name+"/"+rateName(sr), func(t *testing.T) {
				v := factory.make(sr)
				for _, x := range []float64{0, 1, -1, 1e300, math.NaN(), math.Inf(1), math.Inf(-1)} {
					v.NoteOn(x, x)
					v.SetExpression(Expression{x, x, x, x, x, x, x})
					for i := 0; i < sr/100; i++ {
						requireSafeSample(t, v.Next(), i)
					}
					v.NoteOff()
				}
				// Recover after hostile controls and exercise fast gestures during sustain.
				v.NoteOn(220, .8)
				for i := 0; i < sr; i++ {
					if i%127 == 0 {
						e := normalExpression(110 + float64(i%880))
						e.Pressure = float64(i%101) / 100
						e.Drive = float64(i%97) / 96
						e.Vibrato = 45
						v.SetExpression(e)
					}
					requireSafeSample(t, v.Next(), i)
				}
				v.NoteOff()
				for i := 0; i < sr/4; i++ {
					requireSafeSample(t, v.Next(), i)
				}
			})
		}
	}
}
func rateName(sr int) string {
	switch sr {
	case 8000:
		return "8k"
	case 44100:
		return "44k1"
	case 48000:
		return "48k"
	case 96000:
		return "96k"
	case 192000:
		return "192k"
	}
	return "other"
}

func TestVoiceAudibleAndReleases(t *testing.T) {
	const sr = 48000
	for _, factory := range voiceFactories {
		t.Run(factory.name, func(t *testing.T) {
			v := factory.make(sr)
			v.SetExpression(normalExpression(220))
			v.NoteOn(220, .8)
			var on float64
			for i := 0; i < sr; i++ {
				x := v.Next()
				requireSafeSample(t, x, i)
				if i >= sr/2 {
					on += x * x
				}
			}
			on = math.Sqrt(on / float64(sr/2))
			if on < 1e-5 {
				t.Fatalf("inaudible sustained/plucked signal: RMS %g", on)
			}
			v.NoteOff()
			var off float64
			for i := 0; i < 3*sr; i++ {
				x := v.Next()
				requireSafeSample(t, x, i)
				if i >= 5*sr/2 {
					off += x * x
				}
			}
			off = math.Sqrt(off / float64(sr/2))
			t.Logf("RMS before release %.6g, after 2.5 s %.6g", on, off)
			if off > on*.1+1e-6 {
				t.Errorf("release did not decay by 20 dB: on %g off %g", on, off)
			}
		})
	}
}

func TestVoiceRealtimeNoAllocations(t *testing.T) {
	for _, factory := range voiceFactories {
		t.Run(factory.name, func(t *testing.T) {
			v := factory.make(48000)
			v.NoteOn(220, .8)
			e := normalExpression(220)
			allocs := testing.AllocsPerRun(100, func() {
				v.SetExpression(e)
				for i := 0; i < 128; i++ {
					v.Next()
				}
			})
			if allocs != 0 {
				t.Fatalf("audio/control path allocated %g objects per 128 frames", allocs)
			}
		})
	}
}

func TestVoiceChunkSchedulingIsDeterministic(t *testing.T) {
	// The API is scalar. This verifies identical sample-timed gestures when a host
	// divides calls into differing blocks, including a non-power-of-two boundary.
	const n = 16000
	for _, factory := range voiceFactories {
		t.Run(factory.name, func(t *testing.T) {
			render := func(block int) []float64 {
				v := factory.make(48000)
				out := make([]float64, n)
				for start := 0; start < n; start += block {
					end := start + block
					if end > n {
						end = n
					}
					for i := start; i < end; i++ {
						switch i {
						case 0:
							v.SetExpression(normalExpression(220))
							v.NoteOn(220, .8)
						case 4093:
							e := normalExpression(330)
							e.Vibrato = 20
							v.SetExpression(e)
						case 8191:
							v.NoteOff()
						case 12003:
							v.NoteOn(165, .6)
						}
						out[i] = v.Next()
					}
				}
				return out
			}
			want := render(1)
			for _, block := range []int{64, 127, 512} {
				got := render(block)
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("block %d diverged at %d: %g != %g", block, i, got[i], want[i])
					}
				}
			}
		})
	}
}

// Pitch is measured in a settled window with normalized autocorrelation. Logs
// are measurements, not a perceptual-realism claim or an unvalidated threshold.
func TestVoicePitchMeasurements(t *testing.T) {
	const sr = 48000
	for _, factory := range voiceFactories {
		t.Run(factory.name, func(t *testing.T) {
			for _, hz := range []float64{110, 220, 440} {
				v := factory.make(sr)
				e := normalExpression(hz)
				e.Drive = 0
				e.Damping = 0
				v.SetExpression(e)
				v.NoteOn(hz, .8)
				for i := 0; i < sr/3; i++ {
					v.Next()
				}
				samples := make([]float64, sr/5)
				for i := range samples {
					samples[i] = v.Next()
				}
				measured, correlation := measurePitch(samples, sr, hz)
				t.Logf("requested %.2f Hz: local fundamental %.3f Hz, error %+.2f cents, correlation %.5f", hz, measured, 1200*math.Log2(measured/hz), correlation)
			}
		})
	}
}
func measurePitch(x []float64, sr int, expected float64) (float64, float64) {
	lo := int(float64(sr) / expected * .8)
	hi := int(float64(sr) / expected * 1.2)
	scores := make([]float64, hi+2)
	for lag := lo - 1; lag <= hi+1; lag++ {
		var dot, a, b float64
		for i := hi + 1; i < len(x); i++ {
			u, v := x[i], x[i-lag]
			dot += u * v
			a += u * u
			b += v * v
		}
		if a*b > 0 {
			scores[lag] = dot / math.Sqrt(a*b)
		}
	}
	best := lo
	for i := lo + 1; i <= hi; i++ {
		if scores[i] > scores[best] {
			best = i
		}
	}
	frac := 0.0
	den := scores[best-1] - 2*scores[best] + scores[best+1]
	if den != 0 {
		frac = .5 * (scores[best-1] - scores[best+1]) / den
	}
	if math.Abs(frac) > 1 {
		frac = 0
	}
	return float64(sr) / (float64(best) + frac), scores[best]
}

func BenchmarkVoice(b *testing.B) {
	for _, factory := range voiceFactories {
		b.Run(factory.name, func(b *testing.B) {
			v := factory.make(48000)
			v.SetExpression(normalExpression(220))
			v.NoteOn(220, .8)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v.Next()
			}
		})
	}
}

func TestVoiceSilentBeforeNoteAndAtZeroVelocity(t *testing.T) {
	for _, factory := range voiceFactories {
		t.Run(factory.name, func(t *testing.T) {
			for _, sr := range []int{-1, 0, 48000, 1000000} {
				v := factory.make(sr)
				for i := 0; i < 1024; i++ {
					if x := v.Next(); x != 0 {
						t.Fatalf("new voice emits %g at %d (requested rate %d)", x, i, sr)
					}
				}
				v.SetExpression(normalExpression(220))
				v.NoteOn(220, 0)
				for i := 0; i < 8192; i++ {
					if x := v.Next(); math.Abs(x) > 1e-10 {
						t.Fatalf("zero velocity emits %g at %d", x, i)
					}
				}
			}
		})
	}
}

func TestVoiceContinuousPitchBend(t *testing.T) {
	const sr = 48000
	for _, factory := range voiceFactories {
		t.Run(factory.name, func(t *testing.T) {
			v := factory.make(sr)
			e := normalExpression(220)
			e.Drive = 0
			e.Damping = 0
			v.SetExpression(e)
			v.NoteOn(220, .8)
			for i := 0; i < sr/4; i++ {
				v.Next()
			}
			// An expression-only two-semitone bend must retune the existing vibration.
			// There is deliberately no second NoteOn to hide a retrigger in this check.
			target := 220 * math.Exp2(2.0/12)
			e.PitchHz = target
			v.SetExpression(e)
			for i := 0; i < sr/3; i++ {
				v.Next()
			}
			x := make([]float64, sr/5)
			for i := range x {
				x[i] = v.Next()
			}
			measured, corr := measurePitch(x, sr, target)
			cents := 1200 * math.Log2(measured/target)
			t.Logf("expression-only bend %.3f -> %.3f Hz: %+.2f cents corr %.5f", 220.0, measured, cents, corr)
			if math.Abs(cents) > 20 || corr < .8 {
				t.Fatalf("bend failed to reach target %.3f Hz: %.3f Hz corr %.5f", target, measured, corr)
			}
		})
	}
}
