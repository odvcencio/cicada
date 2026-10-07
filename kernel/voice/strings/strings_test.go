package strings

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"
)

func TestValidation(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		if _, err := New(rate, DefaultParams()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := New(32000, DefaultParams()); err == nil {
		t.Fatal("accepted rate")
	}
	for _, change := range []func(*Params){
		func(p *Params) { p.Release = math.NaN() }, func(p *Params) { p.Cutoff = math.Inf(1) },
		func(p *Params) { p.Attack = 0 }, func(p *Params) { p.Ensemble = 2 },
		func(p *Params) { p.Octave4 = -1 }, func(p *Params) { p.Octave16, p.Octave8, p.Octave4 = 0, 0, 0 },
	} {
		p := DefaultParams()
		change(&p)
		if _, err := New(48000, p); err == nil {
			t.Fatal("accepted invalid params", p)
		}
	}
	i, _ := New(48000, DefaultParams())
	for _, n := range []uint8{0, 20, 109, 255} {
		if i.NoteOn(n, 100) == nil {
			t.Fatal("accepted note", n)
		}
	}
	if i.NoteOn(60, 255) == nil || i.SetSustain(float32(math.NaN())) == nil || i.SetSustain(2) == nil {
		t.Fatal("accepted velocity or pedal")
	}
	if _, err := Patch("factory"); err == nil {
		t.Fatal("accepted unknown patch")
	}
}

func TestFiniteAndRelease(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		i, _ := New(rate, DefaultParams())
		for _, n := range []uint8{21, 36, 48, 60, 72, 84, 96, 108} {
			_ = i.NoteOn(n, 127)
		}
		var power, stereo float64
		for range rate / 2 {
			l, r := i.NextStereo()
			if l != l || r != r || math.Abs(float64(l)) > 8 || math.Abs(float64(r)) > 8 {
				t.Fatalf("rate=%d unstable (%g,%g)", rate, l, r)
			}
			power += float64(l*l + r*r)
			stereo += float64((l - r) * (l - r))
		}
		if power < .01 || stereo < .001 {
			t.Fatal("silent instrument or ensemble")
		}
		i.AllNotesOff()
		for range rate * 3 {
			i.NextStereo()
		}
		if i.Active() {
			t.Fatal("release did not retire voices")
		}
	}
}

func TestDivideDownPitch(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	for note := 21; note <= 96; note++ {
		low, high := i.pitches[note-21], i.pitches[note+12-21]
		if low.class != high.class || low.shift != high.shift+1 || low.delta*2 != high.delta {
			t.Fatalf("notes %d and %d do not share divide-down generator", note, note+12)
		}
		freq := float64(low.delta) * 48000
		want := 440 * math.Pow(2, (float64(note)-69)/12)
		cents := math.Abs(1200 * math.Log2(freq/want))
		if cents > .001 {
			t.Fatalf("note=%d pitch error %.6f cents", note, cents)
		}
	}
	for range 1024 {
		i.NextStereo()
	}
	phase := i.phases[0]
	_ = i.NoteOn(60, 100)
	if phase != i.phases[0] {
		t.Fatal("note-on reset shared oscillator phase")
	}
}

func TestSustainVelocityAndStealing(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	_ = i.NoteOn(60, 100)
	_ = i.SetSustain(1)
	i.NoteOff(60)
	if i.voices[0].stage == 3 {
		t.Fatal("sustain failed")
	}
	_ = i.SetSustain(0)
	if i.voices[0].stage != 3 {
		t.Fatal("pedal up did not release")
	}
	for n := uint8(40); n < 49; n++ {
		_ = i.NoteOn(n, 100)
	}
	for _, v := range i.voices {
		if v.stage != 0 && (v.note == 60 || v.note == 40) {
			t.Fatal("stealing did not prefer released/oldest voice")
		}
	}
	_ = i.NoteOn(48, 0)
	for _, v := range i.voices {
		if v.note == 48 && v.held {
			t.Fatal("velocity zero failed")
		}
	}
	p := DefaultParams()
	p.Ensemble, p.Velocity = 0, 1
	energy := func(velocity uint8) float64 {
		i, _ := New(48000, p)
		_ = i.NoteOn(60, velocity)
		var power float64
		for range 12000 {
			l, _ := i.NextStereo()
			power += float64(l * l)
		}
		return power
	}
	if energy(100) < energy(50)*3.8 {
		t.Fatal("velocity failed")
	}
}

func TestAllocationFree(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	if allocations := testing.AllocsPerRun(50, func() {
		_ = i.SetVoiceLimit(8)
		for n := uint8(48); n < 60; n++ {
			_ = i.NoteOn(n, 100)
		}
		_ = i.SetSustain(1)
		i.NoteOff(60)
		for range 128 {
			i.NextStereo()
		}
		i.AllNotesOff()
	}); allocations != 0 {
		t.Fatalf("trigger/render allocated %g times", allocations)
	}
}

func TestGolden(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	h := fnv.New64a()
	var data [8]byte
	for frame := 0; frame < 16384; frame++ {
		switch frame {
		case 0:
			_ = i.NoteOn(48, 76)
			_ = i.NoteOn(60, 105)
		case 2048:
			_ = i.SetSustain(1)
			i.NoteOff(48)
		case 4096:
			for n := uint8(40); n < 50; n++ {
				_ = i.NoteOn(n, 100)
			}
		case 8192:
			i.AllNotesOff()
		}
		l, r := i.NextStereo()
		binary.LittleEndian.PutUint32(data[:4], math.Float32bits(l))
		binary.LittleEndian.PutUint32(data[4:], math.Float32bits(r))
		_, _ = h.Write(data[:])
	}
	const want uint64 = 0xa04e27d4fe442884
	if h.Sum64() != want {
		t.Fatalf("golden=%016x want=%016x", h.Sum64(), want)
	}
}

func TestReset(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	_ = i.NoteOn(60, 100)
	for range 2048 {
		i.NextStereo()
	}
	i.Reset()
	for range 512 {
		l, r := i.NextStereo()
		if l != 0 || r != 0 || i.Active() {
			t.Fatal("reset left a note or ensemble tail")
		}
	}
	i.Reset()
	fresh, _ := New(48000, DefaultParams())
	_ = i.NoteOn(60, 100)
	_ = fresh.NoteOn(60, 100)
	for range 4096 {
		a, b := i.NextStereo()
		c, d := fresh.NextStereo()
		if a != c || b != d {
			t.Fatal("reset did not restore generator state")
		}
	}
}

func BenchmarkStringMachine(b *testing.B) {
	for _, count := range []int{1, 8} {
		name := "one_voice"
		if count == 8 {
			name = "eight_voices"
		}
		b.Run(name, func(b *testing.B) {
			i, _ := New(48000, DefaultParams())
			for n := 0; n < count; n++ {
				_ = i.NoteOn(uint8(48+n*3), 100)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for range 128 {
					i.NextStereo()
				}
			}
		})
	}
}

func TestEnsembleRingBoundary(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	i.baseDelay = [3]float32{1e-7, 1e-7, 1e-7}
	i.slowDepth, i.fastDepth = [3]float32{}, 0
	i.NextStereo()
}

func TestVoiceLimit(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	if i.voiceLimit != MaxVoices {
		t.Fatal("default polyphony is not eight")
	}
	for _, invalid := range []int{-1, 0, 9} {
		if i.SetVoiceLimit(invalid) == nil {
			t.Fatal("accepted voice limit", invalid)
		}
	}
	for n := uint8(40); n < 48; n++ {
		_ = i.NoteOn(n, 100)
	}
	_ = i.SetVoiceLimit(2)
	for _, v := range i.voices[2:] {
		if v.stage != 0 {
			t.Fatal("reducing polyphony left a voice outside the limit")
		}
	}
	for limit := 1; limit <= MaxVoices; limit++ {
		_ = i.SetVoiceLimit(limit)
		i.Reset()
		if i.voiceLimit != limit {
			t.Fatal("reset changed voice limit")
		}
		for n := uint8(40); n < 56; n++ {
			_ = i.NoteOn(n, 100)
			if n%2 == 0 {
				i.NoteOff(n)
			}
			active := 0
			for _, v := range i.voices {
				if v.stage != 0 {
					active++
				}
			}
			if active > limit {
				t.Fatalf("limit=%d active=%d", limit, active)
			}
			i.NextStereo()
		}
	}
}
