package sample

import (
	"math"
	"testing"
)

func mappedZone(value float32, layer, group, position, count uint8, release bool) Zone {
	pcm := make([]float32, 8192)
	for i := range pcm {
		pcm[i] = value
	}
	return Zone{Region: Region{Left: pcm, SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: !release, LoopEnd: len(pcm)}, KeyLow: 48, KeyHigh: 72, VelocityLow: 1, VelocityHigh: 127, Layer: layer, Group: group, Position: position, Count: count, Release: release, Gain: 1}
}
func newMapped(t *testing.T, z []Zone, c InstrumentConfig) *Instrument {
	t.Helper()
	p, err := NewInstrument(48000, z, c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func settled(p *Instrument) float32 {
	var x float32
	for i := 0; i < 128; i++ {
		x, _ = p.NextStereo()
	}
	return x
}

func TestInstrumentVelocityCrossfadeAndSilence(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	p := newMapped(t, []Zone{mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 0, 0, 1, false)}, c)
	previous := float32(0)
	for vel := uint8(0); vel < 128; vel++ {
		p.Reset()
		_, err := p.NoteOn(60, vel)
		if err != nil {
			t.Fatal(err)
		}
		got := settled(p)
		want := float64(vel) / 127 * (.2 + .6*max(0, min(1, float64(int(vel)-32)/64)))
		if math.Abs(float64(got)-want) > 1e-6 {
			t.Fatalf("velocity %d: %g != %g", vel, got, want)
		}
		if got < previous {
			t.Fatal("nonmonotonic fixture response")
		}
		previous = got
	}
	p.Reset()
	if l, r := p.NextStereo(); l != 0 || r != 0 {
		t.Fatal("inactive noise")
	}
}

func TestInstrumentRoundRobinPedalReleaseAndOwnership(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	c.Amp.Release = 10
	z := []Zone{mappedZone(.2, 64, 0, 0, 2, false), mappedZone(.4, 64, 0, 1, 2, false), mappedZone(.1, 64, 0, 0, 2, true), mappedZone(.3, 64, 0, 1, 2, true)}
	p := newMapped(t, z, c)
	h, _ := p.NoteOn(60, 127)
	if x := settled(p); math.Abs(float64(x)-.2) > 1e-6 {
		t.Fatal(x)
	}
	second, _ := p.NoteOn(60, 127)
	if p.NoteOff(h) {
		t.Fatal("stale owner released replacement")
	}
	if x := settled(p); math.Abs(float64(x)-.4) > 1e-6 {
		t.Fatal(x)
	}
	p.Sustain(true)
	p.NoteOff(second)
	settled(p)
	if p.voices[0].off || p.voices[0].releaseStarted {
		t.Fatal("pedal released early")
	}
	p.Sustain(false)
	if !p.voices[0].off || !p.voices[0].releaseStarted {
		t.Fatal("pedal up missed release")
	}
	if p.voices[0].release[0].region.Left[0] != .3 {
		t.Fatal("release take did not match attack")
	}
	p.NoteOff(second)
	for i := 0; i < 9000; i++ {
		p.NextStereo()
	}
	if p.ActiveVoices() != 0 {
		t.Fatal("release remained active")
	}
	if l, r := p.NextStereo(); l != 0 || r != 0 {
		t.Fatal("post release noise")
	}
}

func TestInstrumentStealAndLoopDiscontinuities(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	p := newMapped(t, []Zone{mappedZone(.8, 64, 0, 0, 1, false)}, c)
	p.NoteOn(60, 127)
	settled(p)
	p.NoteOn(60, 127)
	prev := float32(.8)
	var largest float64
	for i := 0; i < 500; i++ {
		x, _ := p.NextStereo()
		largest = max(largest, math.Abs(float64(x-prev)))
		prev = x
	}
	if largest > .03 {
		t.Fatalf("steal step %g", largest)
	}
	r := mappedZone(0, 64, 0, 0, 1, false).Region
	r.LoopStart = 128
	r.LoopEnd = 512
	r.Crossfade = 128
	for i := 128; i < 512; i++ {
		r.Left[i] = float32(i-128) / 384
	}
	v, err := New(48000, r)
	if err != nil {
		t.Fatal(err)
	}
	v.NoteOn(60, 127)
	prev = 0
	largest = 0
	for i := 0; i < 2048; i++ {
		x, _ := v.NextStereo()
		largest = max(largest, math.Abs(float64(x-prev)))
		prev = x
	}
	if largest > .03 {
		t.Fatalf("loop step %g", largest)
	}
}

func TestInstrumentEnvelopeLegatoHumanizationAndAllocations(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Amp = Envelope{Attack: 10, Decay: 20, Sustain: .5, Release: 30}
	c.Cutoff = 300
	c.FilterDepth = 18000
	c.Filter = Envelope{Attack: 8, Decay: 20, Sustain: .2, Release: 15}
	c.Humanize = Humanize{Seed: 1234, DelayMS: 3, Velocity: 4, Cents: 3}
	z := []Zone{mappedZone(.5, 64, 0, 0, 1, false)}
	a, b := newMapped(t, z, c), newMapped(t, z, c)
	ah, _ := a.NoteOn(60, 100)
	bh, _ := b.NoteOn(60, 100)
	var al, ar, bl, br [2048]float32
	a.Render(al[:], ar[:])
	for i := 0; i < 2048; i += 64 {
		b.Render(bl[i:i+64], br[i:i+64])
	}
	for i := range al {
		if al[i] != bl[i] || ar[i] != br[i] || !finite(float64(al[i])) {
			t.Fatalf("determinism/finite at %d", i)
		}
	}
	phase := a.voices[ah.Slot].attack[0].phase
	if err := a.Legato(ah, 62, 4); err != nil {
		t.Fatal(err)
	}
	if a.voices[ah.Slot].attack[0].phase != phase {
		t.Fatal("legato reset phase")
	}
	if err := a.Legato(ah, 90, 0); err == nil {
		t.Fatal("cross-zone legato accepted")
	}
	a.NoteOff(ah)
	b.NoteOff(bh)
	for i := 0; i < 1440; i++ {
		a.NextStereo()
	}
	if a.voices[ah.Slot].amp.level != 0 || a.ActiveVoices() != 0 {
		t.Fatal("release timing")
	}
	a.Reset()
	a.NoteOn(60, 100)
	if n := testing.AllocsPerRun(100, func() { a.Render(al[:128], ar[:128]) }); n != 0 {
		t.Fatalf("render allocs %g", n)
	}
	if n := testing.AllocsPerRun(100, func() { h, _ := a.NoteOn(60, 100); a.NoteOff(h); a.NextStereo() }); n != 0 {
		t.Fatalf("note allocs %g", n)
	}
}

func TestInstrumentRejectsBrokenMaps(t *testing.T) {
	c := DefaultInstrumentConfig()
	z := mappedZone(.2, 64, 0, 0, 2, false)
	if _, err := NewInstrument(48000, []Zone{z}, c); err == nil {
		t.Fatal("incomplete cycle")
	}
	z.Count = 1
	if _, err := NewInstrument(48000, []Zone{z, z}, c); err == nil {
		t.Fatal("duplicate zone")
	}
	other := z
	other.Group = 1
	if _, err := NewInstrument(48000, []Zone{z, other}, c); err == nil {
		t.Fatal("ambiguous group")
	}
	c.Amp.Release = math.NaN()
	if _, err := NewInstrument(48000, []Zone{z}, c); err == nil {
		t.Fatal("NaN accepted")
	}
}

func BenchmarkInstrumentEightVoices(b *testing.B) {
	c := DefaultInstrumentConfig()
	c.Voices = 8
	p, err := NewInstrument(48000, []Zone{mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.5, 96, 0, 0, 1, false)}, c)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		p.NoteOn(60, 64)
	}
	var l, r [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Render(l[:], r[:])
	}
}

func TestInstrumentOneShotAndHatChoke(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 2
	a := mappedZone(.5, 64, 0, 0, 1, false)
	a.OneShot = true
	a.ChokeGroup = 1
	a.KeyLow = 60
	a.KeyHigh = 60
	b := mappedZone(.2, 64, 1, 0, 1, false)
	b.OneShot = true
	b.ChokeGroup = 1
	b.KeyLow = 61
	b.KeyHigh = 61
	p := newMapped(t, []Zone{a, b}, c)
	h, _ := p.NoteOn(60, 127)
	settled(p)
	p.NoteOff(h)
	if p.voices[h.Slot].off {
		t.Fatal("one shot obeyed key-up")
	}
	p.NoteOn(61, 127)
	for i := 0; i < 96; i++ {
		p.NextStereo()
	}
	if p.voices[h.Slot].active {
		t.Fatal("open hat survived choke")
	}
}

func TestInstrumentEventSeedDoesNotDependOnOtherTriggers(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Humanize = Humanize{Seed: 42, Velocity: 10, Cents: 10, DelayMS: 5}
	z := []Zone{mappedZone(.3, 64, 0, 0, 1, false)}
	a, b := newMapped(t, z, c), newMapped(t, z, c)
	for i := 0; i < 19; i++ {
		b.jitter()
	}
	before := b.random
	ah, _ := a.NoteOnSeeded(60, 64, 123456789)
	bh, _ := b.NoteOnSeeded(60, 64, 123456789)
	av, bv := a.voices[ah.Slot], b.voices[bh.Slot]
	if b.random != before || av.velocity != bv.velocity || av.delay != bv.delay || av.attack[0].Ratio() != bv.attack[0].Ratio() {
		t.Fatal("event seed depends on preceding stream or consumes default randomness")
	}
}
