package fm

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func mustInstrument(t testing.TB, rate int, patch string) *Instrument {
	t.Helper()
	p, err := Patch(patch)
	if err != nil {
		t.Fatal(err)
	}
	f, err := New(rate, p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func renderEnergy(f *Instrument, frames int) float64 {
	var energy float64
	for range frames {
		l, r := f.NextStereo()
		energy += float64(l)*float64(l) + float64(r)*float64(r)
	}
	return energy / float64(frames)
}

func TestPatchesKeyboardFiniteAndBounded(t *testing.T) {
	for _, name := range []string{"fm_ep", "bell_keys", "fm_bass"} {
		for _, rate := range []int{44100, 48000, 96000, 192000} {
			f := mustInstrument(t, rate, name)
			maximum := 0.
			for note := MinNote; note <= MaxNote; note++ {
				for _, velocity := range []uint8{1, 64, 127} {
					f.AllNotesOff()
					if err := f.NoteOn(uint8(note), velocity); err != nil {
						t.Fatal(err)
					}
					peak := 0.
					for frame := 0; frame < rate/100; frame++ {
						l, r := f.NextStereo()
						if math.IsNaN(float64(l+r)) || math.IsInf(float64(l+r), 0) {
							t.Fatalf("patch=%s rate=%d note=%d nonfinite", name, rate, note)
						}
						peak = max(peak, math.Abs(float64(l)), math.Abs(float64(r)))
					}
					if peak == 0 || peak > 5 {
						t.Fatalf("patch=%s rate=%d note=%d velocity=%d peak=%g", name, rate, note, velocity, peak)
					}
					maximum = max(maximum, peak)
				}
			}
			t.Logf("patch=%s rate=%d maximum_peak=%.5f", name, rate, maximum)
		}
	}
}

func TestPatchValidation(t *testing.T) {
	for _, rate := range []int{0, -48000, 22050, 88200} {
		if _, err := New(rate, DefaultParams()); err == nil {
			t.Fatalf("accepted rate %d", rate)
		}
	}
	if _, err := Patch("unknown"); err == nil {
		t.Fatal("accepted unknown patch")
	}
	mutations := []func(*Params){
		func(p *Params) { p.Gain = float32(math.NaN()) },
		func(p *Params) { p.StereoSpread = 1.1 },
		func(p *Params) { p.Operators[0].Ratio = 0 },
		func(p *Params) { p.Operators[1].Level = 9 },
		func(p *Params) { p.Operators[1].Detune = 51 },
		func(p *Params) { p.Operators[1].Velocity = -1 },
		func(p *Params) { p.Operators[1].Feedback = float32(math.Inf(1)) },
		func(p *Params) { p.Operators[1].Envelope.Release = 0 },
		func(p *Params) { p.Operators[1].Envelope.Sustain = 1.1 },
		func(p *Params) { p.Operators[1].Envelope.Attack = -1 },
		func(p *Params) { p.Routing[3][0] = 1 },
		func(p *Params) { p.Routing[0][1] = -1 },
		func(p *Params) { p.Output = [Operators]float32{} },
	}
	for i, mutate := range mutations {
		p := DefaultParams()
		mutate(&p)
		if _, err := New(48000, p); err == nil {
			t.Fatalf("accepted invalid mutation %d", i)
		}
	}
	f := mustInstrument(t, 48000, "fm_ep")
	for _, pair := range [][2]uint8{{20, 100}, {109, 100}, {60, 128}} {
		if err := f.NoteOn(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted note/velocity %v", pair)
		}
	}
	for _, value := range []float32{-1, 1.1, float32(math.NaN())} {
		if err := f.SetSustain(value); err == nil {
			t.Fatalf("accepted sustain %g", value)
		}
	}
}

func TestVelocityChangesLevelAndTimbre(t *testing.T) {
	for _, name := range []string{"fm_ep", "bell_keys", "fm_bass"} {
		var previous float64
		var brightness [2]float64
		for i, velocity := range []uint8{32, 127} {
			f := mustInstrument(t, 48000, name)
			_ = f.NoteOn(60, velocity)
			var energy, difference, last float64
			for range 12000 {
				l, r := f.NextStereo()
				value := float64(l+r) * .5
				energy += value * value
				difference += (value - last) * (value - last)
				last = value
			}
			if energy <= previous {
				t.Fatalf("patch=%s velocity=%d energy=%g previous=%g", name, velocity, energy, previous)
			}
			previous = energy
			brightness[i] = difference / energy
		}
		if brightness[1] < brightness[0]*1.1 {
			t.Fatalf("patch=%s lacks velocity brightness: %v", name, brightness)
		}
		t.Logf("patch=%s normalized_difference_soft=%.6f hard=%.6f ratio=%.3f", name, brightness[0], brightness[1], brightness[1]/brightness[0])
	}
}

func TestReleaseSustainAndStealing(t *testing.T) {
	f := mustInstrument(t, 48000, "fm_ep")
	_ = f.NoteOn(60, 100)
	_ = f.SetSustain(1)
	_ = renderEnergy(f, 4800)
	f.NoteOff(60)
	if f.voices[0].ops[0].envelope.stage == 3 {
		t.Fatal("pedal failed to hold released key")
	}
	_ = f.SetSustain(0)
	if f.voices[0].ops[0].envelope.stage != 3 {
		t.Fatal("pedal release failed to start release")
	}
	_ = renderEnergy(f, 48000)
	if f.voices[0].key != nil {
		t.Fatal("release left an active voice")
	}
	if e := renderEnergy(f, 1000); e != 0 {
		t.Fatalf("release left energy %g", e)
	}
	for i := range MaxVoices {
		_ = f.NoteOn(uint8(48+i), 100)
	}
	f.NoteOff(52)
	_ = f.NoteOn(72, 100)
	found := false
	for _, v := range f.voices {
		if v.key != nil && v.note == 52 {
			t.Fatal("did not steal released voice")
		}
		if v.key != nil && v.note == 72 {
			found = true
		}
	}
	if !found {
		t.Fatal("new voice is missing")
	}
	_ = f.NoteOn(73, 100)
	for _, v := range f.voices {
		if v.key != nil && v.note == 48 {
			t.Fatal("did not steal oldest held voice")
		}
	}
	_ = f.NoteOn(72, 110)
	count := 0
	for _, v := range f.voices {
		if v.key != nil && v.note == 72 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("retrigger voice count=%d", count)
	}
	_ = f.NoteOn(72, 0)
	for _, v := range f.voices {
		if v.key != nil && v.note == 72 && v.held {
			t.Fatal("zero velocity did not release key")
		}
	}
	f.AllNotesOff()
	_ = renderEnergy(f, 48000)
	for _, v := range f.voices {
		if v.key != nil {
			t.Fatal("AllNotesOff left active voice")
		}
	}
}

func TestHeldDecayAndFrequencyGuard(t *testing.T) {
	f := mustInstrument(t, 48000, "bell_keys")
	_ = f.NoteOn(60, 127)
	attack := renderEnergy(f, 4800)
	_ = renderEnergy(f, 48000*2)
	late := renderEnergy(f, 4800)
	if late >= attack*.1 {
		t.Fatalf("bell decay too slow: attack=%g late=%g", attack, late)
	}
	for _, name := range []string{"fm_ep", "bell_keys", "fm_bass"} {
		p := mustInstrument(t, 48000, name)
		for n, k := range p.keys {
			for i, o := range k.ops {
				if float64(o.phaseStep)*96000/4294967296. >= 48000*.4 {
					t.Fatalf("patch=%s note=%d op=%d frequency exceeds guard", name, n+MinNote, i)
				}
			}
		}
	}
}

func TestDecimatorStopBand(t *testing.T) {
	f := mustInstrument(t, 48000, "fm_ep")
	response := func(frequency float64) float64 {
		var re, im float64
		for i, value := range f.fir {
			for _, index := range []int{i, firSize - 1 - i} {
				a := 2 * math.Pi * frequency * float64(index) / 96000
				re += float64(value) * math.Cos(a)
				im -= float64(value) * math.Sin(a)
			}
		}
		return math.Hypot(re, im)
	}
	if pass := response(14000); pass < .99 {
		t.Fatalf("pass band loss=%g", pass)
	}
	maximum := 0.
	for hz := 24000.; hz < 48000; hz += 97 {
		maximum = max(maximum, response(hz))
	}
	if maximum > .0001 {
		t.Fatalf("stop band maximum=%g dB=%g", maximum, 20*math.Log10(maximum))
	}
	t.Logf("decimator_stop_band_maximum_db=%.2f", 20*math.Log10(maximum))
}

func TestTriggerAndRenderAllocations(t *testing.T) {
	f := mustInstrument(t, 48000, "fm_ep")
	allocs := testing.AllocsPerRun(100, func() {
		_ = f.NoteOn(60, 100)
		_ = f.SetSustain(1)
		for range 128 {
			f.NextStereo()
		}
		f.NoteOff(60)
		f.AllNotesOff()
		_ = f.SetSustain(0)
		f.Reset()
	})
	if allocs != 0 {
		t.Fatalf("render/trigger allocations=%g", allocs)
	}
}

func TestResetClearsVoicesPedalAndFilterHistory(t *testing.T) {
	f := mustInstrument(t, 48000, "fm_ep")
	_ = f.NoteOn(60, 100)
	var first [1024][2]float32
	for i := range first {
		first[i][0], first[i][1] = f.NextStereo()
	}
	_ = f.SetSustain(1)
	f.NoteOff(60)
	_ = f.NoteOn(60, 127)
	f.Reset()
	for range 256 {
		l, r := f.NextStereo()
		if l != 0 || r != 0 {
			t.Fatal("reset left audible history")
		}
	}
	if f.sustain != 0 {
		t.Fatal("reset retained pedal")
	}
	_ = f.NoteOn(60, 100)
	for i, want := range first {
		l, r := f.NextStereo()
		if l != want[0] || r != want[1] {
			t.Fatalf("reset changed voice at frame %d", i)
		}
	}
}

func TestConfigurableVoiceLimit(t *testing.T) {
	f := mustInstrument(t, 48000, "fm_ep")
	if f.voiceLimit != MaxVoices {
		t.Fatal("default voice limit changed")
	}
	for _, invalid := range []int{-1, 0, 9} {
		if f.SetVoiceLimit(invalid) == nil {
			t.Fatalf("accepted voice limit %d", invalid)
		}
	}
	for _, limit := range []int{1, 2, 4, 8} {
		f.Reset()
		if err := f.SetVoiceLimit(limit); err != nil {
			t.Fatal(err)
		}
		_ = f.SetSustain(1)
		for note := 48; note < 60; note++ {
			_ = f.NoteOn(uint8(note), 100)
			if note%2 == 0 {
				f.NoteOff(uint8(note))
			}
			f.NextStereo()
			active := 0
			for i, v := range f.voices {
				if v.key != nil {
					active++
					if i >= limit {
						t.Fatalf("active slot %d exceeds limit %d", i, limit)
					}
				}
			}
			if active > limit {
				t.Fatalf("active voices %d exceed limit %d", active, limit)
			}
		}
		f.Reset()
		if f.voiceLimit != limit {
			t.Fatal("Reset changed voice limit")
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { _ = f.SetVoiceLimit(2) }); allocations != 0 {
		t.Fatalf("voice limit allocations=%g", allocations)
	}
}

func TestHighRegisterCarrierHasNoAudibleImages(t *testing.T) {
	p := DefaultParams()
	p.Output = [Operators]float32{1}
	p.Routing = [Operators][Operators]float32{}
	p.Operators[0].Ratio, p.Operators[0].Feedback = 4, 0
	p.Operators[0].Envelope = Envelope{Sustain: 1, Release: .1}
	f, err := New(48000, p)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.NoteOn(108, 127)
	_ = renderEnergy(f, 1024)
	hz := float64(f.keys[MaxNote-MinNote].ops[0].phaseStep) * 96000 / 4294967296.
	const frames = 48000
	var cc, ss, cs, yc, ys, energy float64
	for i := range frames {
		l, r := f.NextStereo()
		y := float64(l+r) * .5
		s, c := math.Sincos(2 * math.Pi * hz * float64(i) / 48000)
		cc += c * c
		ss += s * s
		cs += c * s
		yc += y * c
		ys += y * s
		energy += y * y
	}
	determinant := cc*ss - cs*cs
	c, s := (yc*ss-ys*cs)/determinant, (ys*cc-yc*cs)/determinant
	residual := max(1e-30, energy-c*yc-s*ys)
	db := 10 * math.Log10(residual/energy)
	if db > -90 {
		t.Fatalf("high carrier residual=%g dB", db)
	}
	t.Logf("high_carrier_hz=%.6f total_residual_db=%.2f", hz, db)
}

func goldenRender(t testing.TB, blockSize int) [32]byte {
	f := mustInstrument(t, 48000, "fm_ep")
	hash := sha256.New()
	var bytes [8]byte
	for start := 0; start < 16384; start += blockSize {
		for frame := start; frame < start+blockSize; frame++ {
			switch frame {
			case 0:
				_ = f.NoteOn(21, 127)
				_ = f.NoteOn(60, 75)
				_ = f.NoteOn(108, 100)
			case 1024:
				_ = f.SetSustain(1)
				f.NoteOff(60)
			case 2048:
				_ = f.NoteOn(60, 110)
			case 4096:
				for i := range 10 {
					_ = f.NoteOn(uint8(36+i*5), uint8(40+i*8))
				}
			case 8192:
				f.AllNotesOff()
				_ = f.SetSustain(.5)
			case 12288:
				_ = f.SetSustain(0)
			}
			l, r := f.NextStereo()
			binary.LittleEndian.PutUint32(bytes[:4], math.Float32bits(l))
			binary.LittleEndian.PutUint32(bytes[4:], math.Float32bits(r))
			hash.Write(bytes[:])
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func TestBlockIndependentGolden(t *testing.T) {
	var expected [32]byte
	for _, blockSize := range []int{1, 64, 128, 256} {
		got := goldenRender(t, blockSize)
		if blockSize == 1 {
			expected = got
		} else if got != expected {
			t.Fatalf("block=%d changed golden", blockSize)
		}
	}
	const golden = "a795f342c7e5ece0452f97974c5f52c4cc8e1c1382779a948a7f449526e5341b"
	got := fmt.Sprintf("%x", expected)
	if got != golden {
		t.Fatalf("golden=%s", got)
	}
}

func BenchmarkStereo(b *testing.B) {
	for _, count := range []int{1, 8} {
		b.Run(fmt.Sprintf("voices_%d", count), func(b *testing.B) {
			f := mustInstrument(b, 48000, "fm_ep")
			for i := range count {
				_ = f.NoteOn(uint8(48+i*3), 100)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f.NextStereo()
			}
		})
	}
}
