package piano

import (
	"fmt"
	"math"
	"testing"
)

func mustPiano(t testing.TB, rate int) *Instrument {
	t.Helper()
	p, err := New(rate)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKeyboardFiniteAndBounded(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		p := mustPiano(t, rate)
		var maximum float64
		for note := MinNote; note <= MaxNote; note++ {
			for _, velocity := range []uint8{1, 64, 127} {
				p.Reset()
				if err := p.NoteOn(uint8(note), velocity); err != nil {
					t.Fatal(err)
				}
				peak := 0.
				for frame := 0; frame < rate/3; frame++ {
					l, r := p.NextStereo()
					for _, x := range []float32{l, r} {
						if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
							t.Fatalf("rate=%d note=%d velocity=%d frame=%d nonfinite", rate, note, velocity, frame)
						}
						peak = max(peak, math.Abs(float64(x)))
					}
				}
				if peak <= 1e-8 || peak > 4 {
					t.Fatalf("rate=%d note=%d velocity=%d peak=%g", rate, note, velocity, peak)
				}
				if p.voices[0].contact {
					t.Fatalf("hammer failed to leave note %d", note)
				}
				maximum = max(maximum, peak)
			}
		}
		t.Logf("rate=%d keyboard_max_peak=%g", rate, maximum)
	}
}

func energy(p *Instrument, frames int) float64 {
	var e float64
	for range frames {
		l, r := p.NextStereo()
		e += float64(l)*float64(l) + float64(r)*float64(r)
	}
	return e / float64(frames)
}

func TestHammerVelocityAndContact(t *testing.T) {
	p := mustPiano(t, 48000)
	var previous float64
	for _, velocity := range []uint8{16, 32, 64, 96, 127} {
		p.Reset()
		_ = p.NoteOn(60, velocity)
		e := energy(p, 48000/3)
		if e <= previous {
			t.Fatalf("hammer energy not increasing: velocity=%d energy=%g previous=%g", velocity, e, previous)
		}
		if !p.voices[0].touched || p.voices[0].hammerVelocity >= 0 {
			t.Fatal("hammer did not compress and rebound from the string")
		}
		previous = e
	}
}

func TestRenderedPartialPitch(t *testing.T) {
	p := mustPiano(t, 48000)
	_ = p.NoteOn(60, 110)
	energy(p, 2400)
	pcm := make([]float64, 48000)
	for i := range pcm {
		l, r := p.NextStereo()
		pcm[i] = float64(l+r) * .5 * (.5 - .5*math.Cos(2*math.Pi*float64(i)/float64(len(pcm)-1)))
	}
	for _, partial := range []int{1, 4, 8} {
		expected := PartialFrequency(60, partial)
		best, frequency := 0., 0.
		for cents := -15; cents <= 15; cents++ {
			f := expected * math.Exp2(float64(cents)/1200)
			c, s := math.Cos(2*math.Pi*f/48000), math.Sin(2*math.Pi*f/48000)
			re, im, x, y := 0., 0., 1., 0.
			for _, v := range pcm {
				re += v * x
				im += v * y
				x, y = c*x-s*y, s*x+c*y
			}
			power := re*re + im*im
			if power > best {
				best, frequency = power, f
			}
		}
		errorCents := 1200 * math.Log2(frequency/expected)
		if math.Abs(errorCents) > 4 || best < 1e-8 {
			t.Fatalf("rendered partial %d pitch error=%g cents energy=%g", partial, errorCents, best)
		}
		t.Logf("partial=%d frequency=%.3f expected=%.3f error=%.1f cents", partial, frequency, expected, errorCents)
	}
}

func TestRateIndependentHammerAndRadiation(t *testing.T) {
	var levels [3]float64
	for i, rate := range []int{44100, 48000, 96000} {
		p := mustPiano(t, rate)
		_ = p.NoteOn(60, 100)
		levels[i] = energy(p, rate/2)
	}
	for i, e := range levels {
		db := 10 * math.Log10(e/levels[1])
		if math.Abs(db) > 1 {
			t.Fatalf("rate index=%d energy drift=%g dB", i, db)
		}
	}
}

func TestDispersionAndUnisons(t *testing.T) {
	p := mustPiano(t, 48000)
	for _, note := range []uint8{21, 60, 108} {
		fundamental := PartialFrequency(note, 1)
		if math.Abs(fundamental-noteFrequency(int(note))) > 1e-9 {
			t.Fatal("stiffness detunes fundamental")
		}
		if PartialFrequency(note, 8) <= 8*fundamental {
			t.Fatal("upper partials lack dispersion")
		}
		if PartialFrequency(note, 8)/fundamental <= PartialFrequency(note, 2)/fundamental*4 {
			t.Fatal("dispersion is not increasing with frequency")
		}
		k := &p.keys[note-MinNote]
		for i := 0; i < k.count; i++ {
			if k.modes[i].decay <= 0 || k.modes[i].decay >= 1 {
				t.Fatal("nonpassive mode")
			}
		}
	}
	k := &p.keys[60-MinNote]
	if k.modes[0].s == k.modes[1].s || k.modes[1].s == k.modes[2].s {
		t.Fatal("unison strings are not independently tuned")
	}
}

func TestSustainHalfPedalAndDamper(t *testing.T) {
	var levels [3]float64
	for i, pedal := range []float32{0, .5, 1} {
		p := mustPiano(t, 48000)
		_ = p.SetSustain(pedal)
		_ = p.NoteOn(60, 100)
		energy(p, 12000)
		p.NoteOff(60)
		energy(p, 18000)
		levels[i] = energy(p, 12000)
	}
	if levels[0] >= levels[1] || levels[1] >= levels[2] || levels[2] < levels[0]*100 {
		t.Fatalf("half pedal not ordered: %v", levels)
	}
	p := mustPiano(t, 48000)
	_ = p.SetSustain(1)
	_ = p.NoteOn(60, 100)
	energy(p, 12000)
	p.NoteOff(60)
	energy(p, 12000)
	before := energy(p, 4800)
	_ = p.SetSustain(0)
	energy(p, 48000)
	after := energy(p, 4800)
	if after > before*.001 {
		t.Fatalf("pedal release fails to damp: before=%g after=%g", before, after)
	}
}

func TestSympatheticStringsAndSoundboard(t *testing.T) {
	p := mustPiano(t, 48000)
	_ = p.SetSustain(1)
	_ = p.NoteOn(60, 110)
	energy(p, 6000)
	// A different, unstruck string has received bridge energy.
	r := &p.sympathetic[72-MinNote]
	if r.q == 0 && r.p == 0 {
		t.Fatal("unstruck octave did not resonate")
	}
	boardEnergy := float32(0)
	for i := range p.board {
		boardEnergy += abs(p.board[i].q) + abs(p.board[i].p)
	}
	if boardEnergy == 0 {
		t.Fatal("soundboard has no state")
	}
	clear(p.voices[:])
	if energy(p, 1024) == 0 {
		t.Fatal("shared resonance stops with voice")
	}
	p.Reset()
	if energy(p, 1024) != 0 || p.ActiveVoices() != 0 {
		t.Fatal("reset leaves ringing state")
	}
}

func TestPolyphonyStealingAndRetrigger(t *testing.T) {
	p := mustPiano(t, 48000)
	for note := uint8(48); note < 48+MaxVoices; note++ {
		_ = p.NoteOn(note, 100)
		energy(p, 256)
	}
	if p.ActiveVoices() != MaxVoices {
		t.Fatal("polyphony count")
	}
	_ = p.NoteOn(51, 127)
	if p.ActiveVoices() != MaxVoices || p.voices[3].state[0].q == 0 {
		t.Fatal("retrigger discards vibrating string")
	}
	p.NoteOff(53)
	_ = p.NoteOn(72, 100)
	if p.voices[5].note != 72 || p.ActiveVoices() != MaxVoices {
		t.Fatal("steal did not prefer released key")
	}
	p.AllNotesOff()
	for i := range p.voices {
		if p.voices[i].held {
			t.Fatal("all notes off left held voice")
		}
	}
}

func TestPianoAllocationFree(t *testing.T) {
	p := mustPiano(t, 48000)
	note := uint8(48)
	allocs := testing.AllocsPerRun(100, func() {
		_ = p.NoteOn(note, 100)
		_ = p.SetSustain(.5)
		for range 128 {
			p.NextStereo()
		}
		p.NoteOff(note)
		note++
		if note > 84 {
			note = 48
		}
	})
	if allocs != 0 {
		t.Fatalf("note/pedal/render allocated %g", allocs)
	}
}

func TestRapidSustainedRetriggersRemainFinite(t *testing.T) {
	p := mustPiano(t, 48000)
	_ = p.SetSustain(.7)
	for block := 0; block < 2000; block++ {
		if block%2 == 0 {
			for _, note := range []uint8{48, 60, 64, 67} {
				_ = p.NoteOn(note, 127)
			}
		}
		for range 128 {
			l, r := p.NextStereo()
			if math.IsNaN(float64(l)) || math.IsNaN(float64(r)) || math.IsInf(float64(l), 0) || math.IsInf(float64(r), 0) || abs(l) > 8 || abs(r) > 8 {
				t.Fatalf("unstable retrigger block=%d samples=%g,%g", block, l, r)
			}
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := New(8000); err == nil {
		t.Fatal("invalid rate accepted")
	}
	p := mustPiano(t, 48000)
	for _, args := range [][2]uint8{{20, 100}, {109, 100}, {60, 128}} {
		if p.NoteOn(args[0], args[1]) == nil {
			t.Fatal("invalid note accepted")
		}
	}
	for _, v := range []float32{-1, 2, float32(math.NaN()), float32(math.Inf(1))} {
		if p.SetSustain(v) == nil {
			t.Fatal("invalid pedal accepted")
		}
	}
	_ = p.NoteOn(60, 100)
	_ = p.NoteOn(60, 0)
	if p.voices[0].held {
		t.Fatal("zero velocity did not release")
	}
}

var benchmarkOutput float32

func BenchmarkPianoBlock(b *testing.B) {
	for _, count := range []int{0, 1, 4, 8} {
		b.Run(fmt.Sprintf("voices=%d", count), func(b *testing.B) {
			p := mustPiano(b, 48000)
			strike := func() {
				for i := 0; i < count; i++ {
					_ = p.NoteOn(uint8(48+i*3), 100)
				}
			}
			strike()
			_ = p.SetSustain(1)
			var sum float32
			b.ReportAllocs()
			b.ResetTimer()
			for block := 0; block < b.N; block++ {
				if block%256 == 0 {
					strike()
				}
				for range 128 {
					l, r := p.NextStereo()
					sum += l + r
				}
			}
			benchmarkOutput = sum
		})
	}
}
