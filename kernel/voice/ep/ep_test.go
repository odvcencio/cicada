package ep

import (
	"fmt"
	"math"
	"testing"
)

func energy(p *Instrument, frames int) float64 {
	var e float64
	for range frames {
		l, r := p.NextStereo()
		e += float64(l)*float64(l) + float64(r)*float64(r)
	}
	return e / float64(frames)
}
func TestVelocityTimbreAndDecay(t *testing.T) {
	for _, name := range []string{"tine_ep", "reed_ep"} {
		p, _ := Patch(name)
		p.Drive = 0
		var energies [2]float64
		var bright [2]float64
		for j, vel := range []uint8{32, 120} {
			i, err := New(48000, p)
			if err != nil {
				t.Fatal(err)
			}
			_ = i.NoteOn(60, vel)
			var last float32
			var e, d float64
			for range 12000 {
				l, r := i.NextStereo()
				if math.IsNaN(float64(l)) || math.IsInf(float64(r), 0) {
					t.Fatal("nonfinite PCM")
				}
				e += float64(l) * float64(l)
				delta := float64(l - last)
				d += delta * delta
				last = l
			}
			energies[j] = e
			bright[j] = d / e
			i.AllNotesOff()
			energy(i, 48000)
			if i.Active() != 0 {
				t.Fatalf("%s release left %d voices", name, i.Active())
			}
		}
		if energies[1] <= energies[0]*10 || bright[1] <= bright[0]*1.08 {
			t.Fatalf("%s velocity energy=%v brightness=%v", name, energies, bright)
		}
		t.Logf("%s hard/soft energy=%.2f brightness=%.2f", name, energies[1]/energies[0], bright[1]/bright[0])
	}
}
func TestElectricPianoControlsAndAllocations(t *testing.T) {
	for _, name := range []string{"tine_ep", "tine_bell", "tine_bark", "tine_tremolo", "reed_ep", "reed_tremolo"} {
		p, _ := Patch(name)
		i, err := New(48000, p)
		if err != nil {
			t.Fatal(err)
		}
		if got := testing.AllocsPerRun(20, func() {
			for n := 0; n < 12; n++ {
				_ = i.NoteOn(uint8(48+n*3), 100)
			}
			for range 128 {
				i.NextStereo()
			}
			i.AllNotesOff()
			_ = i.SetSustain(1)
			_ = i.SetSustain(0)
		}); got != 0 {
			t.Fatalf("%s allocated %g", name, got)
		}
		if i.Active() > MaxVoices {
			t.Fatal("voice bound exceeded")
		}
		i.Reset()
		_ = i.SetSustain(1)
		_ = i.NoteOn(60, 90)
		i.NoteOff(60)
		if energy(i, 48000) < 1e-8 {
			t.Fatal("pedal failed")
		}
		_ = i.SetSustain(0)
		energy(i, 48000)
		if i.Active() != 0 {
			t.Fatal("pedal release failed")
		}
	}
}
func TestElectricPianoValidation(t *testing.T) {
	p := DefaultParams()
	for _, rate := range []int{0, 8000, 47999} {
		if _, err := New(rate, p); err == nil {
			t.Fatal("rate accepted")
		}
	}
	for _, x := range []float64{-1, 3, math.NaN(), math.Inf(1)} {
		q := p
		q.PickupDistance = x
		if _, err := New(48000, q); err == nil {
			t.Fatal("pickup accepted")
		}
	}
	i, _ := New(48000, p)
	if i.NoteOn(20, 100) == nil || i.NoteOn(60, 0) == nil || i.SetSustain(float32(math.NaN())) == nil {
		t.Fatal("invalid event accepted")
	}
}
func TestElectricPianoGolden(t *testing.T) {
	// The hash includes dynamics, retrigger, oversampling and damper release.
	for _, name := range []string{"tine_ep", "reed_ep"} {
		p, _ := Patch(name)
		i, _ := New(48000, p)
		var hash uint64 = 14695981039346656037
		for frame := 0; frame < 16384; frame++ {
			switch frame {
			case 0:
				_ = i.NoteOn(60, 45)
			case 2048:
				_ = i.NoteOn(67, 120)
			case 8192:
				i.AllNotesOff()
			}
			l, r := i.NextStereo()
			for _, x := range [2]float32{l, r} {
				bits := math.Float32bits(x)
				for range 4 {
					hash ^= uint64(byte(bits))
					hash *= 1099511628211
					bits >>= 8
				}
			}
		}
		expected := map[string]uint64{"tine_ep": 0x9ade8a6d693b8941, "reed_ep": 0x22794472f05887f9}[name]
		if expected != 0 && hash != expected {
			t.Fatalf("%s golden changed: %016x", name, hash)
		}
		t.Logf("%s golden=%016x", name, hash)
	}
}
func BenchmarkElectricPiano(b *testing.B) {
	for _, name := range []string{"tine_ep", "reed_ep"} {
		for _, n := range []int{1, 8} {
			b.Run(fmt.Sprintf("%s/%d", name, n), func(b *testing.B) {
				p, _ := Patch(name)
				p.Decay = 20
				i, _ := New(48000, p)
				for j := 0; j < n; j++ {
					_ = i.NoteOn(uint8(48+j*3), 100)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for k := 0; k < b.N; k++ {
					if k%1000 == 0 {
						i.Reset()
						for j := 0; j < n; j++ {
							_ = i.NoteOn(uint8(48+j*3), 100)
						}
					}
					for range 128 {
						i.NextStereo()
					}
				}
			})
		}
	}
}

func TestConfiguredVoiceLimit(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	if i.SetVoiceLimit(0) == nil || i.SetVoiceLimit(9) == nil {
		t.Fatal("invalid voice limit accepted")
	}
	for _, limit := range []int{1, 2, 4, 8} {
		i.Reset()
		_ = i.SetVoiceLimit(limit)
		for n := 0; n < 12; n++ {
			_ = i.NoteOn(uint8(36+n*3), 100)
		}
		if i.Active() != limit {
			t.Fatalf("active=%d limit=%d", i.Active(), limit)
		}
		i.Reset()
		for n := 0; n < 12; n++ {
			_ = i.NoteOn(uint8(36+n*3), 100)
		}
		if i.Active() != limit {
			t.Fatal("reset lost voice limit")
		}
	}
}

// TestTineSpectralBalance measures inharmonic energy separately from pickup
// harmonics. A centroid alone cannot distinguish bark from bending modes.
func TestTineSpectralBalance(t *testing.T) {
	// Pre-change reed second harmonics, in dB re f0, in the 10–70 ms window.
	reedSecond := map[uint8][3]float64{
		57: {-16.287, -9.880, -8.596},
		62: {-16.366, -9.924, -8.549},
		64: {-16.396, -9.941, -8.529},
		73: {-16.579, -10.046, -8.450},
	}
	for _, name := range []string{"tine_ep", "tine_bark", "tine_tremolo", "reed_ep"} {
		for _, note := range []uint8{57, 62, 64, 73} { // A3, D4, E4, C#5.
			t.Run(fmt.Sprintf("%s/%d", name, note), func(t *testing.T) {
				p, err := Patch(name)
				if err != nil {
					t.Fatal(err)
				}
				f0 := 440 * math.Exp2(float64(int(note)-69)/12)
				var seconds [3]float64
				for index, velocity := range []uint8{64, 100, 120} {
					i, err := New(48000, p)
					if err != nil {
						t.Fatal(err)
					}
					if err := i.NoteOn(note, velocity); err != nil {
						t.Fatal(err)
					}
					pcm := make([]float64, 3*48000)
					unmodulated := make([]float64, len(pcm))
					for frame := range pcm {
						l, r := i.NextStereo()
						pcm[frame] = (float64(l) + float64(r)) * .5
						// Remove known gain modulation to measure modal decay and
						// tonebar ripple rather than the intended tremolo envelope.
						tremolo := 1 - p.Tremolo*(.5+.5*float64(i.lfoQ))
						unmodulated[frame] = pcm[frame] / tremolo
					}
					fundamental := epPartial(pcm, f0, .01, .07)
					bend := epPartial(pcm, 6.267*f0, .01, .07)
					bendDB := epDB(bend / fundamental)
					seconds[index] = epDB(epPartial(pcm, 2*f0, .01, .07) / fundamental)
					first := epPartial(unmodulated, 6.267*f0, .01, .07)
					last := epPartial(unmodulated, 6.267*f0, .16, .22)
					drop := epDB(first / last)
					// Calibrated once across the fixed presets: the note-scaled
					// 0.6-second cap gives 13.5 dB at A3; pickup saturation
					// gives tine_bark 14.6–14.8 dB at D4/E4. Higher notes fade faster.
					minimumDrop := 15.
					if p.Model == Tine {
						if note == 57 {
							minimumDrop = 13
						} else if name == "tine_bark" && (note == 62 || note == 64) {
							minimumDrop = 14.4
						}
					}
					if drop < minimumDrop {
						t.Errorf("v%d bending-mode 150 ms drop %.2f dB < %.1f", velocity, drop, minimumDrop)
					}
					if velocity == 100 && bendDB > -15 || velocity == 120 && bendDB > -12 {
						t.Errorf("v%d bending mode too loud: %.2f dB re f0", velocity, bendDB)
					}
					if velocity == 100 && (name == "tine_ep" && seconds[index] < -15 || name == "tine_bark" && seconds[index] < -12) {
						t.Errorf("v%d second harmonic too quiet: %.2f dB re f0", velocity, seconds[index])
					}
					if p.Model == Reed {
						at50 := epDB(epPartial(pcm, 6.267*f0, .02, .08) / epPartial(pcm, f0, .02, .08))
						if velocity == 100 && at50 > -20 {
							t.Errorf("reed bending mode at 50 ms %.2f dB > -20", at50)
						}
						if velocity == 100 && math.Abs(seconds[index]-reedSecond[note][index]) > 1 {
							t.Errorf("reed second harmonic moved more than 1 dB: %.2f -> %.2f", reedSecond[note][index], seconds[index])
						}
					} else {
						if ripple := epFundamentalRipple(unmodulated, f0); ripple > 2.5 {
							t.Errorf("v%d fundamental ripple %.2f dB > 2.5", velocity, ripple)
						}
					}
					if note == 62 {
						crossed := false
						for shift := .01; shift <= .3; shift += .01 {
							if epPartial(unmodulated, 6.267*f0, .01+shift, .07+shift) <= first*.1 {
								crossed = true
								break
							}
						}
						if !crossed {
							t.Errorf("v%d D4 bending-mode T20 exceeds 0.3 s", velocity)
						}
					}
					t.Logf("v%d bend=%.2f dB drop150=%.2f dB second=%.2f dB", velocity, bendDB, drop, seconds[index])
				}
				if growth := seconds[2] - seconds[0]; growth < 3 {
					t.Errorf("second-harmonic bark growth v64 to v120 %.2f dB < 3", growth)
				}
			})
		}
	}
}

// epPartial evaluates selected fractional FFT bins directly using the DFT.
// Exact frequencies and equal Hann windows avoid peak tracking, bin rounding,
// and leakage differences between the attack and tail. No third-harmonic gate
// is used because its measured balance depends on the window.
func epPartial(pcm []float64, hz, start, end float64) float64 {
	window := pcm[int(math.Round(start*48000)):int(math.Round(end*48000))]
	var mean, real, imaginary, weight float64
	for _, x := range window {
		mean += x
	}
	mean /= float64(len(window))
	for frame, x := range window {
		w := .5 - .5*math.Cos(2*math.Pi*float64(frame)/float64(len(window)-1))
		s, c := math.Sincos(2 * math.Pi * hz * float64(frame) / 48000)
		real += (x - mean) * w * c
		imaginary -= (x - mean) * w * s
		weight += w
	}
	return 2 * math.Hypot(real, imaginary) / weight
}

func epDB(ratio float64) float64 { return 20 * math.Log10(max(ratio, 1e-15)) }

func epFundamentalRipple(pcm []float64, hz float64) float64 {
	// Fit out the fundamental's exponential decay in dB, leaving tonebar beats.
	var times, levels []float64
	var sx, sy, sxx, sxy float64
	for start := .1; start+.06 <= 3; start += .02 {
		level := epDB(epPartial(pcm, hz, start, start+.06))
		times, levels = append(times, start), append(levels, level)
		sx, sy, sxx, sxy = sx+start, sy+level, sxx+start*start, sxy+start*level
	}
	n := float64(len(times))
	slope := (n*sxy - sx*sy) / (n*sxx - sx*sx)
	low, high := math.Inf(1), math.Inf(-1)
	for index, level := range levels {
		residual := level - slope*times[index]
		low, high = min(low, residual), max(high, residual)
	}
	return high - low
}
