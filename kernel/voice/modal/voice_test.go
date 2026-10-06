package modal

import (
	"fmt"
	"math"
	"math/cmplx"
	"testing"
)

func newTestVoice(t *testing.T, profile Profile, rate int) *Voice {
	t.Helper()
	v, err := NewVoice(profile, rate)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func render(v *Voice, frames int) []float32 {
	out := make([]float32, frames)
	for i := range out {
		out[i] = v.Next()
	}
	return out
}

func rms(data []float32) float64 {
	var power float64
	for _, x := range data {
		power += float64(x) * float64(x)
	}
	return math.Sqrt(power / float64(len(data)))
}

func TestProfileNamesAndValidation(t *testing.T) {
	for p, name := range Names {
		got, ok := ParseProfile(name)
		if !ok || got != Profile(p) {
			t.Fatalf("ParseProfile(%q) = %d, %v", name, got, ok)
		}
		got, ok = ParseTrackKind("model_" + name)
		if !ok || got != Profile(p) {
			t.Fatalf("ParseTrackKind(%q) = %d, %v", name, got, ok)
		}
		if _, ok := ParseTrackKind(name); ok {
			t.Fatalf("plain profile name %q opted into a modeled track", name)
		}
	}
	for _, name := range []string{"", "model_", "model_unknown", "model_Steelpan", "steelpan"} {
		if _, ok := ParseTrackKind(name); ok {
			t.Fatalf("accepted invalid track kind %q", name)
		}
	}
	if _, err := NewVoice(ProfileCount, 48000); err == nil {
		t.Fatal("accepted unsupported profile")
	}
	for _, rate := range []int{-1, 0, 8000, 32000, 192000} {
		if _, err := NewVoice(Steelpan, rate); err == nil {
			t.Fatalf("accepted unsupported sample rate %d", rate)
		}
	}
}

func TestVelocityAndBrightness(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		t.Run(Names[p], func(t *testing.T) {
			v := newTestVoice(t, p, 48000)
			previous := 0.0
			var softUpper, hardUpper float64
			for _, velocity := range []uint8{32, 64, 96, 127} {
				v.Reset()
				v.NoteOn(60, velocity, false)
				s := &v.strikes[v.latest]
				upper := 0.0
				for i := 1; i < s.count; i++ {
					upper += s.modes[i].gain * s.modes[i].gain
				}
				upper /= s.modes[0].gain * s.modes[0].gain
				if velocity == 32 {
					softUpper = upper
				}
				if velocity == 127 {
					hardUpper = upper
				}
				level := rms(render(v, 4800))
				if level <= previous {
					t.Fatalf("RMS %.8f at velocity %d did not exceed %.8f", level, velocity, previous)
				}
				t.Logf("METRIC modal profile=%s velocity=%d rms_dbfs=%.3f", Names[p], velocity, 20*math.Log10(level))
				previous = level
			}
			brightnessDB := 10 * math.Log10(hardUpper/softUpper)
			if brightnessDB < 9 {
				t.Fatalf("upper-mode excitation changed only %.3f dB", brightnessDB)
			}
			t.Logf("METRIC modal profile=%s hard_soft_upper_mode_db=%.3f", Names[p], brightnessDB)
		})
	}
}

func TestDeterministicStrikeVariationAndPitch(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		t.Run(Names[p], func(t *testing.T) {
			v := newTestVoice(t, p, 48000)
			var strikes [4][]float32
			var frequency float64
			for i := range strikes {
				// Isolate the strike while retaining the variation sequence.
				v.strikes, v.latest = [MaxVoices]strike{}, -1
				v.NoteOn(60, 96, false)
				if i == 0 {
					frequency = v.strikes[0].modes[0].freq
				}
				if v.strikes[0].modes[0].freq != frequency {
					t.Fatal("strike variation changed the fundamental")
				}
				strikes[i] = render(v, 2048)
				for previous := 0; previous < i; previous++ {
					difference := 0.0
					for n, x := range strikes[i] {
						d := float64(x - strikes[previous][n])
						difference += d * d
					}
					if difference < 1e-9 {
						t.Fatalf("strikes %d and %d are indistinguishable", i, previous)
					}
				}
			}
			v.Reset()
			for i := range strikes {
				v.strikes, v.latest = [MaxVoices]strike{}, -1
				v.NoteOn(60, 96, false)
				for n, want := range strikes[i] {
					if got := v.Next(); got != want {
						t.Fatalf("reset sequence strike %d frame %d = %g, want %g", i, n, got, want)
					}
				}
			}
		})
	}
}

func TestRenderedVelocitySpectralResponse(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		var balance [2]float64
		for i, velocity := range []uint8{32, 127} {
			v := newTestVoice(t, p, 48000)
			v.NoteOn(60, velocity, false)
			data := spectrum(render(v, 1<<14))
			// Integrate above the first mode's neighbourhood instead of
			// inspecting gain parameters. This includes the complete contact,
			// transient and modal tail in the emitted waveform.
			boundary := v.pitch(60) * recipes[p].ratio[0] * 1.35
			var low, upper float64
			for bin := 1; bin < len(data)/2; bin++ {
				magnitude := cmplx.Abs(data[bin])
				power := magnitude * magnitude
				if float64(bin)*48000/float64(len(data)) < boundary {
					low += power
				} else {
					upper += power
				}
			}
			balance[i] = upper / low
		}
		changeDB := 10 * math.Log10(balance[1]/balance[0])
		t.Logf("METRIC modal profile=%s rendered_hard_soft_upper_band_db=%.3f", Names[p], changeDB)
		if changeDB < 3 {
			t.Fatalf("%s hard strike increased relative upper-band energy by only %.3f dB", Names[p], changeDB)
		}
	}
}

func TestModalPolesAndBandwidth(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for p := Profile(0); p < ProfileCount; p++ {
			for _, note := range []uint8{0, 36, 60, 84, 108, 127} {
				v := newTestVoice(t, p, rate)
				v.NoteOn(note, 127, false)
				s := &v.strikes[v.latest]
				for i := 0; i < s.count; i++ {
					m := &s.modes[i]
					frequency := math.Atan2(m.s, m.c) * float64(rate) / (2 * math.Pi)
					cents := 1200 * math.Log2(frequency/m.freq)
					if math.Abs(cents) > 5 {
						t.Fatalf("%s rate=%d note=%d mode=%d tuning %.8f cents", Names[p], rate, note, i, cents)
					}
					if frequency >= .45*float64(rate) {
						t.Fatalf("%s rate=%d note=%d mode=%d exceeds modal bandwidth", Names[p], rate, note, i)
					}
					if radius := math.Hypot(m.c, m.s); radius >= 1 || radius <= 0 {
						t.Fatalf("unstable modal pole radius %g", radius)
					}
				}
			}
		}
	}
}

func TestAudibleFundamentalFFT(t *testing.T) {
	// A rendered, windowed spectrum is independent of the pole calculation.
	// The shaker is unpitched; short impact surfaces have broad, transient
	// spectra, so their pole tuning is checked separately above.
	for _, p := range []Profile{Steelpan, Marimba, Vibraphone, Conga, Zinc} {
		v := newTestVoice(t, p, 48000)
		v.NoteOn(60, 127, false)
		data := render(v, 1<<16)
		frequency := fftPeak(data, 48000, 440*math.Exp2(-9.0/12))
		cents := 1200 * math.Log2(frequency/(440*math.Exp2(-9.0/12)))
		t.Logf("METRIC modal profile=%s rendered_tuning_cents=%.5f", Names[p], cents)
		if math.Abs(cents) > 5 {
			t.Fatalf("%s rendered fundamental %.3f cents from authored note", Names[p], cents)
		}
	}
}

func TestNaturalReleaseAndVibraphoneDamper(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		v := newTestVoice(t, p, 48000)
		v.NoteOn(60, 127, false)
		render(v, 4800)
		control := *v
		v.NoteOff()
		if p != Vibraphone {
			for n := 0; n < 4800; n++ {
				if v.Next() != control.Next() {
					t.Fatalf("%s key-up shortened the natural tail", Names[p])
				}
			}
			continue
		}
		render(v, 4800)
		render(&control, 4800)
		ratioDB := 20 * math.Log10(rms(render(v, 960))/rms(render(&control, 960)))
		t.Logf("METRIC modal profile=vibraphone damper_after_100ms_db=%.3f", ratioDB)
		if ratioDB > -70 {
			t.Fatalf("vibraphone damper leaves %.3f dB at 100 ms", ratioDB)
		}
	}
}

func TestSlideRetainsEnergyAndDoesNotRetrigger(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		v := newTestVoice(t, p, 48000)
		v.NoteOn(48, 127, false)
		render(v, 720)
		before := v.strikes[v.latest]
		sequence := v.sequence
		v.NoteOn(60, 64, true)
		after := &v.strikes[v.latest]
		if after.age != before.age || after.excitation != before.excitation || v.sequence != sequence || v.ActiveVoices() != 1 {
			t.Fatalf("%s slide retriggered the contact", Names[p])
		}
		for i := 0; i < after.count; i++ {
			if after.modes[i].x != before.modes[i].x || after.modes[i].y != before.modes[i].y {
				t.Fatalf("%s slide discarded resonator energy", Names[p])
			}
		}
		for n := 0; n < 144; n++ {
			if x := v.Next(); math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				t.Fatalf("%s slide produced non-finite output", Names[p])
			}
			for i := 0; i < after.count; i++ {
				m := &after.modes[i]
				if math.Hypot(m.c, m.s) > m.radius+1e-12 {
					t.Fatal("slide interpolation created an unstable pole")
				}
			}
		}
		if after.glide != 0 {
			t.Fatal("slide did not reach its destination")
		}
	}
}

func TestSilenceFiniteTailsAndBoundedState(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		v := newTestVoice(t, p, 48000)
		v.NoteOn(60, 0, false)
		for n := 0; n < 256; n++ {
			if v.Next() != 0 {
				t.Fatalf("%s velocity zero excited silence", Names[p])
			}
		}
		for hit := 0; hit < 20; hit++ {
			v.NoteOn(uint8(36+hit*4), 127, false)
			if v.ActiveVoices() > MaxVoices {
				t.Fatal("voice count exceeds its fixed bound")
			}
			for n := 0; n < 128; n++ {
				x := float64(v.Next())
				if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 4 {
					t.Fatalf("%s invalid output %g", Names[p], x)
				}
			}
		}
		v.Reset()
		v.NoteOn(60, 127, false)
		var sum, peak float64
		for n := 0; n < 16*48000; n++ {
			x := float64(v.Next())
			sum += x
			peak = max(peak, math.Abs(x))
		}
		if v.ActiveVoices() != 0 || v.Next() != 0 {
			t.Fatalf("%s retained an unbounded tail", Names[p])
		}
		dcDB := 20 * math.Log10(math.Abs(sum/(16*48000))/peak)
		t.Logf("METRIC modal profile=%s full_tail_dc_db_relative_peak=%.3f tail_after_16s=0", Names[p], dcDB)
		if dcDB > -50 {
			t.Fatalf("%s excessive full-tail DC %.3f dB", Names[p], dcDB)
		}
		v.NoteOn(60, 127, false)
		v.Reset()
		for n := 0; n < 256; n++ {
			if v.Next() != 0 {
				t.Fatalf("%s reset did not give exact zero", Names[p])
			}
		}
	}
}

func TestDecayT60(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		v := newTestVoice(t, p, 48000)
		v.NoteOn(60, 127, false)
		s := &v.strikes[v.latest]
		// Isolate one physical mode after its finite excitation. This tests
		// the actual render recurrence, including its amplitude decay.
		render(v, s.excitation+1)
		for i := 1; i < s.count; i++ {
			s.modes[i].active = false
		}
		m := &s.modes[0]
		before := math.Hypot(m.x, m.y)
		frames := int(math.Round(recipes[p].t60[0] * v.rate))
		for n := 0; n < frames; n++ {
			v.Next()
		}
		decayDB := 20 * math.Log10(math.Hypot(m.x, m.y)/before)
		t.Logf("METRIC modal profile=%s fundamental_t60_s=%.3f decay_at_t60_db=%.6f", Names[p], recipes[p].t60[0], decayDB)
		if math.Abs(decayDB+60) > .002 {
			t.Fatalf("%s decay at T60 is %.6f dB", Names[p], decayDB)
		}
	}
}

func TestRetainedTailAgainst96kReference(t *testing.T) {
	// Start the reference from the same post-contact continuous oscillator
	// state. Two 96 kHz rotations must match one 48 kHz rotation. Only modes
	// retained below the 48 kHz bandwidth participate. This bounds unwanted
	// oscillator products and float32 noise in the linear tail; it does not
	// qualify the broadband contact or an acoustic recording comparison.
	for p := Profile(0); p < ProfileCount; p++ {
		for _, note := range []uint8{60, 108} {
			v := newTestVoice(t, p, 48000)
			v.NoteOn(note, 127, false)
			s := &v.strikes[v.latest]
			render(v, s.excitation+1)
			ref := newTestVoice(t, p, 96000)
			ref.strikes[0], ref.latest = *s, 0
			r := &ref.strikes[0]
			r.excitation = 0
			for i := 0; i < r.count; i++ {
				m := &r.modes[i]
				m.radius = math.Sqrt(m.radius)
				m.s, m.c = math.Sincos(2 * math.Pi * m.freq / ref.rate)
				m.c, m.s = m.c*m.radius, m.s*m.radius
			}
			var signal, residual float64
			for n := 0; n < 16384; n++ {
				ref.Next()
				want, got := float64(ref.Next()), float64(v.Next())
				difference := got - want
				signal += want * want
				residual += difference * difference
			}
			db := 10 * math.Log10(residual/signal)
			t.Logf("METRIC modal profile=%s note=%d retained_tail_96k_residual_db=%.3f", Names[p], note, db)
			if db > -110 {
				t.Fatalf("%s note=%d tail residual %.3f dB exceeds -110 dB", Names[p], note, db)
			}
		}
	}
}

func TestCallbackDoesNotAllocate(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		v := newTestVoice(t, p, 48000)
		allocs := testing.AllocsPerRun(100, func() {
			v.Reset()
			for hit := 0; hit < MaxVoices+1; hit++ {
				v.NoteOn(uint8(60+hit), 96, false)
			}
			for n := 0; n < 128; n++ {
				v.Next()
			}
			v.NoteOn(72, 96, true)
			v.NoteOff()
		})
		if allocs != 0 {
			t.Fatalf("%s callback allocated %.2f times", Names[p], allocs)
		}
	}
}

func BenchmarkVoice(b *testing.B) {
	for p := Profile(0); p < ProfileCount; p++ {
		for _, voices := range []int{1, MaxVoices} {
			b.Run(fmt.Sprintf("%s/%d", Names[p], voices), func(b *testing.B) {
				v, _ := NewVoice(p, 48000)
				for hit := 0; hit < voices; hit++ {
					v.NoteOn(uint8(60+hit), 127, false)
				}
				contactEnd := 0
				for i := range v.strikes {
					contactEnd = max(contactEnd, v.strikes[i].excitation)
				}
				for i := 0; i <= contactEnd; i++ {
					v.Next()
				}
				// Hold the initialized mode energies for a stable active-voice
				// workload. Otherwise fast-decaying surfaces eventually turn
				// this into a misleading silence benchmark.
				for i := range v.strikes {
					for j := 0; j < v.strikes[i].count; j++ {
						m := &v.strikes[i].modes[j]
						m.c, m.s = m.c/m.radius, m.s/m.radius
						m.radius = 1
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					for frame := 0; frame < 128; frame++ {
						v.Next()
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*128), "ns/sample")
			})
		}
	}
}

func fftPeak(samples []float32, rate, target float64) float64 {
	data := spectrum(samples)
	n := len(data)
	bin := int(math.Round(target * float64(n) / rate))
	peak := bin
	for i := bin - 8; i <= bin+8; i++ {
		if cmplx.Abs(data[i]) > cmplx.Abs(data[peak]) {
			peak = i
		}
	}
	a, center, c := math.Log(cmplx.Abs(data[peak-1])), math.Log(cmplx.Abs(data[peak])), math.Log(cmplx.Abs(data[peak+1]))
	offset := .5 * (a - c) / (a - 2*center + c)
	return (float64(peak) + offset) * rate / float64(n)
}

func spectrum(samples []float32) []complex128 {
	data := make([]complex128, len(samples))
	for i, x := range samples {
		window := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(len(samples)-1))
		data[i] = complex(float64(x)*window, 0)
	}
	n := len(data)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}
	for span := 2; span <= n; span <<= 1 {
		root := cmplx.Rect(1, -2*math.Pi/float64(span))
		for base := 0; base < n; base += span {
			rotation := complex(1, 0)
			for i := 0; i < span/2; i++ {
				even, odd := data[base+i], data[base+i+span/2]*rotation
				data[base+i], data[base+i+span/2] = even+odd, even-odd
				rotation *= root
			}
		}
	}
	return data
}
