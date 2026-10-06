package sample

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestInstrumentChokeFadesRecordedRelease(t *testing.T) {
	a := mappedZone(.5, 64, 0, 0, 1, false)
	a.ChokeGroup, a.KeyLow, a.KeyHigh = 1, 60, 60
	release := mappedZone(.25, 64, 0, 0, 1, true)
	release.ChokeGroup, release.KeyLow, release.KeyHigh = 1, 60, 60
	control := mappedZone(0, 64, 1, 0, 1, false)
	control.ChokeGroup, control.KeyLow, control.KeyHigh = 1, 61, 61
	p := newMapped(t, []Zone{a, release, control}, DefaultInstrumentConfig())
	h, err := p.NoteOn(60, 127)
	if err != nil {
		t.Fatal(err)
	}
	settled(p)
	p.NoteOff(h)
	settled(p)
	if !p.voices[h.Slot].release[0].Active() {
		t.Fatal("missing release fixture")
	}
	if _, err := p.NoteOn(61, 127); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 96; i++ {
		p.NextStereo()
	}
	if l, r := p.NextStereo(); l != 0 || r != 0 {
		t.Fatalf("choked release still audible: %g/%g", l, r)
	}
	if p.voices[h.Slot].release[0].Active() {
		t.Fatal("choked release remained active")
	}
}

func TestInstrumentRejectedLegatoPreservesAllLayers(t *testing.T) {
	a, b := mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 0, 0, 1, false)
	a.KeyLow, a.KeyHigh, b.KeyLow, b.KeyHigh = 24, 60, 24, 60
	b.Region.RootKey = 24
	p := newMapped(t, []Zone{a, b}, DefaultInstrumentConfig())
	h, err := p.NoteOn(48, 64)
	if err != nil {
		t.Fatal(err)
	}
	settled(p)
	before := p.voices[h.Slot]
	if err := p.Legato(h, 60, 100); err == nil {
		t.Fatal("accepted unsupported second-layer ratio")
	}
	if !reflect.DeepEqual(before, p.voices[h.Slot]) {
		t.Fatal("failed legato changed voice state")
	}
}

func TestInstrumentReleasePreservesNoteTuning(t *testing.T) {
	for _, legato := range []bool{false, true} {
		for _, pedal := range []bool{false, true} {
			c := DefaultInstrumentConfig()
			c.Humanize = Humanize{Seed: 42, Cents: 10}
			p := newMapped(t, []Zone{mappedZone(.5, 64, 0, 0, 1, false), mappedZone(.25, 64, 0, 0, 1, true)}, c)
			h, err := p.NoteOn(60, 127)
			if err != nil {
				t.Fatal(err)
			}
			settled(p)
			if legato {
				if err := p.Legato(h, 62, 4); err != nil {
					t.Fatal(err)
				}
			}
			want := p.voices[h.Slot].attack[0].Ratio()
			if want == 1 {
				t.Fatal("fixture has no pitch offset")
			}
			p.Sustain(pedal)
			p.NoteOff(h)
			if pedal {
				p.Sustain(false)
			}
			if got := p.voices[h.Slot].release[0].Ratio(); got != want {
				t.Fatalf("legato=%v pedal=%v: release ratio %g, attack %g", legato, pedal, got, want)
			}
		}
	}
}

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

func TestInstrumentCymbalOverlapAndDedicatedChoke(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 8
	crash := mappedZone(.2, 64, 0, 0, 1, false)
	crash.KeyLow, crash.KeyHigh, crash.ChokeGroup = 60, 60, 2
	crash.OneShot, crash.ChokeSustain = true, true
	splash := crash
	splash.KeyLow, splash.KeyHigh, splash.Group, splash.ChokeGroup = 61, 61, 1, 3
	choke := crash
	choke.KeyLow, choke.KeyHigh, choke.Group = 62, 62, 2
	choke.ChokeSustain, choke.Gain = false, 0
	p := newMapped(t, []Zone{crash, splash, choke}, c)
	first, _ := p.NoteOn(60, 127)
	settled(p)
	second, _ := p.NoteOn(60, 127)
	other, _ := p.NoteOn(61, 127)
	if x := settled(p); math.Abs(float64(x)-.6) > 1e-6 {
		t.Fatalf("overlapping cymbals: %g", x)
	}
	p.NoteOff(first)
	if p.voices[first.Slot].off || p.voices[second.Slot].off {
		t.Fatal("one-shot cymbals lost their natural decay")
	}
	p.NoteOn(62, 127)
	for i := 0; i < 96; i++ {
		p.NextStereo()
	}
	if p.voices[first.Slot].active || p.voices[second.Slot].active || !p.voices[other.Slot].active {
		t.Fatal("dedicated choke must damp every crash and preserve the splash")
	}
	if x := settled(p); math.Abs(float64(x)-.2) > 1e-6 {
		t.Fatalf("choke leaked PCM: %g", x)
	}
}

func TestInstrumentSilentChokePreservesUnrelatedSaturatedVoices(t *testing.T) {
	for _, crashes := range []int{0, 15} {
		t.Run(fmt.Sprintf("crashes=%d", crashes), func(t *testing.T) {
			c := DefaultInstrumentConfig()
			c.Voices = 16
			ride := mappedZone(.2, 64, 0, 0, 1, false)
			ride.KeyLow, ride.KeyHigh, ride.ChokeGroup = 51, 51, 7
			ride.OneShot, ride.ChokeSustain = true, true
			crash := ride
			crash.KeyLow, crash.KeyHigh, crash.Group, crash.ChokeGroup = 49, 49, 1, 2
			control := crash
			control.KeyLow, control.KeyHigh, control.Group = 26, 26, 2
			control.Gain, control.ChokeSustain = 0, false
			p := newMapped(t, []Zone{ride, crash, control}, c)
			var rides []Handle
			for i := 0; i < 16-crashes; i++ {
				h, err := p.NoteOn(51, 127)
				if err != nil {
					t.Fatal(err)
				}
				rides = append(rides, h)
			}
			for i := 0; i < crashes; i++ {
				if _, err := p.NoteOn(49, 127); err != nil {
					t.Fatal(err)
				}
			}
			settled(p)
			h, err := p.NoteOn(26, 127)
			if err != nil {
				t.Fatal(err)
			}
			if h.ID != 0 {
				t.Fatal("silent choke allocated a voice")
			}
			for i := 0; i < 96; i++ {
				p.NextStereo()
			}
			for _, h := range rides {
				if p.owned(h) == nil || p.voices[h.Slot].off {
					t.Fatal("silent crash choke stole an unrelated ride")
				}
			}
			if got := p.ActiveVoices(); got != len(rides) {
				t.Fatalf("got %d voices, want %d rides", got, len(rides))
			}
			if x := settled(p); math.Abs(float64(x)-.2*float64(len(rides))) > 1e-6 {
				t.Fatalf("ride output %g", x)
			}
		})
	}
}

func TestInstrumentRejectsInconsistentChokeSustain(t *testing.T) {
	a := mappedZone(.2, 64, 0, 0, 2, false)
	b := mappedZone(.3, 64, 0, 1, 2, false)
	b.ChokeSustain = true
	if _, err := NewInstrument(48000, []Zone{a, b}, DefaultInstrumentConfig()); err == nil {
		t.Fatal("a round-robin cycle cannot alternate choke behavior")
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
