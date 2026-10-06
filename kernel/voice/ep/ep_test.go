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
		expected := map[string]uint64{"tine_ep": 0x2fbc5d7a6c0a6a31, "reed_ep": 0x4945223fda6ec6c9}[name]
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
