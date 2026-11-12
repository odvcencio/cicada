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

// recordedLevel is the level that two unity-gain layers, .2 recorded at
// velocity 32 and .8 at 96, give a note: the soft take scaled by velocity/32
// below its centre, the crossfade between the centres, the loud take above.
func recordedLevel(vel uint8) float64 {
	switch {
	case vel < 32:
		return .2 * float64(vel) / 32
	case vel > 96:
		return .8
	}
	return .2 + .6*float64(int(vel)-32)/64
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
		want := recordedLevel(vel)
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

// A recorded soft layer is already soft. Velocity chooses and crossfades the
// layers and must not scale them again: at a layer's centre the take plays at
// its recorded level, the same in a release zone and on a drum kit lane.
// Before this rule the soft take at velocity 32 played at 32/127 of its
// recorded level (-12 dB), so a pack's recorded range was widened by up to
// 20*log10(127/softest centre) dB.
func TestInstrumentRecordedLayersPlayAtTheirRecordedLevel(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	soft, loud := mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 0, 0, 1, false)
	// A zero-gain choke command on another key does not shape any level.
	command := mappedZone(0, 64, 1, 0, 1, false)
	command.KeyLow, command.KeyHigh, command.ChokeGroup = 80, 80, 1
	for name, mix := range map[string][2]float64{"track": {1, 1}, "kit lane": {.5, .25}} {
		p := newMapped(t, []Zone{soft, loud, command}, c)
		for _, vel := range []uint8{1, 16, 32, 64, 96, 120, 127} {
			p.Reset()
			if _, err := p.NoteOnMix(60, vel, mix[0], mix[1]); err != nil {
				t.Fatal(err)
			}
			var l, r float32
			for i := 0; i < 128; i++ {
				l, r = p.NextStereo()
			}
			want := recordedLevel(vel)
			if math.Abs(float64(l)-want*mix[0]) > 1e-6 || math.Abs(float64(r)-want*mix[1]) > 1e-6 {
				t.Errorf("%s velocity %d: %g/%g, want %g/%g", name, vel, l, r, want*mix[0], want*mix[1])
			}
		}
	}
	// The release takes follow the same rule.
	attack := mappedZone(0, 32, 0, 0, 1, false)
	louder := mappedZone(0, 96, 0, 0, 1, false)
	softRelease, loudRelease := mappedZone(.2, 32, 0, 0, 1, true), mappedZone(.8, 96, 0, 0, 1, true)
	for _, vel := range []uint8{16, 32, 64, 127} {
		p := newMapped(t, []Zone{attack, louder, softRelease, loudRelease}, c)
		h, err := p.NoteOn(60, vel)
		if err != nil {
			t.Fatal(err)
		}
		settled(p)
		p.NoteOff(h)
		got := settled(p)
		if want := recordedLevel(vel); math.Abs(float64(got)-want) > 1e-6 {
			t.Errorf("release velocity %d: %g, want %g", vel, got, want)
		}
	}
}

// A key with one recorded layer has no recorded dynamics, and a pack that sets
// its own zone gains has shaped its velocity response against the documented
// velocity/127 multiplier (cicada-keys-pack sets each gain to 127/centre).
// Both keep that multiplier exactly.
func TestInstrumentShapedAndSingleLayerPacksKeepVelocityOver127(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	single := newMapped(t, []Zone{mappedZone(.5, 64, 0, 0, 1, false)}, c)
	soft, loud := mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 0, 0, 1, false)
	soft.Gain, loud.Gain = 127.0/32, 127.0/96
	shaped := newMapped(t, []Zone{soft, loud}, c)
	for vel := uint8(1); vel < 128; vel++ {
		single.Reset()
		if _, err := single.NoteOn(60, vel); err != nil {
			t.Fatal(err)
		}
		if got, want := settled(single), .5*float64(vel)/127; math.Abs(float64(got)-want) > 1e-6 {
			t.Fatalf("single layer velocity %d: %g, want %g", vel, got, want)
		}
		shaped.Reset()
		if _, err := shaped.NoteOn(60, vel); err != nil {
			t.Fatal(err)
		}
		w := max(0, min(1, float64(int(vel)-32)/64))
		want := (1-w)*.2*soft.Gain*float64(vel)/127 + w*.8*loud.Gain*float64(vel)/127
		if got := settled(shaped); math.Abs(float64(got)-want) > 1e-6 {
			t.Fatalf("shaped pack velocity %d: %g, want %g", vel, got, want)
		}
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

// The declick tail of a stolen voice is the old voice's output after its own
// mix. A new voice with a quiet mix must not silence the tail.
func TestStealTailKeepsTheOldVoiceMix(t *testing.T) {
	pcm := make([]float32, 1024)
	for i := range pcm {
		pcm[i] = .25
	}
	config := DefaultInstrumentConfig()
	config.Voices, config.Amp.Release = 1, 2
	zone := Zone{Region: Region{Left: pcm, SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: true, LoopEnd: len(pcm)}, KeyLow: 60, KeyHigh: 60, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Count: 1, Gain: 1}
	instrument, err := NewInstrument(48000, []Zone{zone}, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instrument.NoteOnMix(60, 127, 1, 1); err != nil {
		t.Fatal(err)
	}
	var l, r float32
	for i := 0; i < 256; i++ {
		l, r = instrument.NextStereo()
	}
	if l < .2 {
		t.Fatalf("first note is not sounding: %g", l)
	}
	if _, err := instrument.NoteOnMix(60, 127, 0, 0); err != nil {
		t.Fatal(err)
	}
	l, r = instrument.NextStereo()
	if l < .2 || r < .2 {
		t.Fatalf("steal tail was scaled by the new voice's mix: %g/%g", l, r)
	}
	for i := 0; i < 200; i++ {
		l, r = instrument.NextStereo()
	}
	if l != 0 || r != 0 {
		t.Fatalf("tail did not end: %g/%g", l, r)
	}
}

// settledLevel plays one note at vel and returns its settled level, or the
// settled level of its release takes when release is set.
func settledLevel(t *testing.T, p *Instrument, vel uint8, release bool) float64 {
	t.Helper()
	p.Reset()
	h, err := p.NoteOn(60, vel)
	if err != nil {
		t.Fatal(err)
	}
	level := settled(p)
	if release {
		p.NoteOff(h)
		level = settled(p)
	}
	return float64(level)
}

// A release with one recorded layer in a pack with recorded dynamics follows
// the attack: min(1, velocity/centre), not velocity/127. The catalog bass has
// four attack layers (24, 56, 88, 120) and one release layer at 64; with the
// old law its release fell 14 dB against its attack at velocity 24.
func TestInstrumentSingleReleaseLayerFollowsTheRecordedLaw(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	var zones []Zone
	for _, layer := range []uint8{24, 56, 88, 120} {
		zones = append(zones, mappedZone(0, layer, 0, 0, 1, false))
	}
	zones = append(zones, mappedZone(.3, 64, 0, 0, 1, true))
	p := newMapped(t, zones, c)
	for _, vel := range []uint8{1, 24, 32, 64, 100, 127} {
		want := .3 * min(1, float64(vel)/64)
		if got := settledLevel(t, p, vel, true); math.Abs(got-want) > 1e-6 {
			t.Errorf("release at velocity %d: %g, want %g", vel, got, want)
		}
	}
}

// A release takes its note's attack law. An attack with one recorded layer
// keeps velocity/127, so its release does too: with min(1, velocity/centre)
// the release at a centre of 64 played up to 6 dB louder against its attack
// than before the recorded law (docs/sampler/packs.md).
func TestInstrumentReleaseFollowsAOneLayerAttack(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	p := newMapped(t, []Zone{mappedZone(0, 64, 0, 0, 1, false), mappedZone(.3, 64, 0, 0, 1, true)}, c)
	for _, vel := range []uint8{1, 24, 32, 64, 100, 127} {
		want := .3 * float64(vel) / 127
		if got := settledLevel(t, p, vel, true); math.Abs(got-want) > 1e-6 {
			t.Errorf("release at velocity %d: %g, want %g", vel, got, want)
		}
	}
}

// A pack's own humanize moves a played velocity. With recorded dynamics the
// moved velocity is kept whenever an attack recording of the note covers it,
// even across a layer boundary into another group's layer: the layers blend
// continuously there, so the move changes the level as it would anywhere
// else. Layers 1-63 (group 0, centre 32) and 64-127 (group 1, centre 96);
// written velocity 62 with humanize 12.
func TestInstrumentRecordedHumanizeCrossesGroupsContinuously(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	c.Humanize.Velocity = 12
	soft, loud := mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 1, 0, 1, false)
	soft.VelocityLow, soft.VelocityHigh = 1, 63
	loud.VelocityLow, loud.VelocityHigh = 64, 127
	p := newMapped(t, []Zone{soft, loud}, c)
	level := func(v float64) float64 { w := (v - 32) / 64; return .2*(1-w) + .8*w }
	crossed := 0
	for seed := uint64(1); seed <= 64; seed++ {
		p.Reset()
		if _, err := p.NoteOnSeeded(60, 62, seed); err != nil {
			t.Fatal(err)
		}
		got := float64(settled(p))
		if got < level(50)-1e-6 || got > level(74)+1e-6 {
			t.Fatalf("seed %d: level %g is outside velocities 50 to 74 (%g to %g)", seed, got, level(50), level(74))
		}
		if got > level(63)+1e-6 {
			crossed++
		}
	}
	if crossed == 0 {
		t.Fatal("no humanized velocity crossed into the loud layer's group; the move was undone")
	}
}

// The other half of the rule: a pack whose zone gains differ has shaped its
// own velocity response, and a humanize move into another group's zone is
// undone, so the written velocity plays. Same layout as above with the loud
// zone at gain 2: no note plays the loud zone, and the notes whose move was
// undone play exactly the written velocity's level.
func TestInstrumentShapedHumanizeStaysInItsGroup(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	c.Humanize.Velocity = 12
	soft, loud := mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 1, 0, 1, false)
	soft.VelocityLow, soft.VelocityHigh = 1, 63
	loud.VelocityLow, loud.VelocityHigh, loud.Gain = 64, 127, 2
	p := newMapped(t, []Zone{soft, loud}, c)
	level := func(v float64) float64 { return .2 * v / 127 }
	undone := 0
	for seed := uint64(1); seed <= 64; seed++ {
		p.Reset()
		if _, err := p.NoteOnSeeded(60, 62, seed); err != nil {
			t.Fatal(err)
		}
		got := float64(settled(p))
		if got > level(63)+1e-6 {
			t.Fatalf("seed %d: level %g is above the soft zone at velocity 63 (%g): a move into the loud group was kept", seed, got, level(63))
		}
		if math.Abs(got-level(62)) < 1e-6 {
			undone++
		}
	}
	if undone == 0 {
		t.Fatal("no note played the written velocity's level; the test needs moves into the loud group")
	}
}

// Layers count per note, not per velocity: a pack whose layers split the
// velocity range blends the neighbouring centres across the split, so the
// level is continuous and rises with velocity. Two layouts: ranges 1-63 and
// 64-127 (centres 32 and 96), and overlapping ranges 1-80 and 60-127 (centres
// 40 and 100). Below the softest centre the level is velocity/centre, which
// steps more than 1.5 dB only between the lowest velocities (6 dB from 1 to 2),
// as velocity/127 always has; the check starts at velocity 8.
func TestInstrumentSplitVelocityRangesStayContinuous(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	for _, layout := range []struct {
		name                  string
		softLow, softHigh, sc uint8
		loudLow, loudHigh, lc uint8
	}{{"split at 64", 1, 63, 32, 64, 127, 96}, {"overlap 60-80", 1, 80, 40, 60, 127, 100}} {
		soft, loud := mappedZone(.2, layout.sc, 0, 0, 1, false), mappedZone(.8, layout.lc, 0, 0, 1, false)
		soft.VelocityLow, soft.VelocityHigh = layout.softLow, layout.softHigh
		loud.VelocityLow, loud.VelocityHigh = layout.loudLow, layout.loudHigh
		p := newMapped(t, []Zone{soft, loud}, c)
		previous := 0.0
		for vel := uint8(1); vel < 128; vel++ {
			got := settledLevel(t, p, vel, false)
			if got < previous-1e-9 {
				t.Errorf("%s: level falls from %g to %g at velocity %d", layout.name, previous, got, vel)
			}
			if vel >= 8 && previous > 0 && 20*math.Log10(got/previous) > 1.5 {
				t.Errorf("%s: level steps %.2f dB at velocity %d", layout.name, 20*math.Log10(got/previous), vel)
			}
			previous = got
		}
		for vel, want := range map[uint8]float64{layout.sc: .2, layout.lc: .8, 127: .8} {
			if got := settledLevel(t, p, vel, false); math.Abs(got-want) > 1e-6 {
				t.Errorf("%s: velocity %d plays %g, want the recorded %g", layout.name, vel, got, want)
			}
		}
	}
}

// The recorded law applies when every sounding zone shares one gain, within
// one part in a million, and that common gain still applies. Zones whose
// gains differ mean the pack compensates for velocity/127, and keep it.
func TestInstrumentRecordedLawNeedsOneCommonGain(t *testing.T) {
	c := DefaultInstrumentConfig()
	c.Voices = 1
	layers := func(softGain, loudGain float64) *Instrument {
		soft, loud := mappedZone(.2, 32, 0, 0, 1, false), mappedZone(.8, 96, 0, 0, 1, false)
		soft.Gain, loud.Gain = softGain, loudGain
		return newMapped(t, []Zone{soft, loud}, c)
	}
	for _, check := range []struct {
		name       string
		soft, loud float64
		vel        uint8
		want       float64
	}{
		{"common gain 0.5", .5, .5, 32, .2 * .5},
		{"common gain 0.5", .5, .5, 16, .2 * .5 * 16 / 32},
		{"gains within 1e-6", 1, 1 + 1e-9, 32, .2},
		{"gains differ", 1, .999, 16, .2 * 16 / 127},
	} {
		if got := settledLevel(t, layers(check.soft, check.loud), check.vel, false); math.Abs(got-check.want) > 1e-6 {
			t.Errorf("%s, velocity %d: %g, want %g", check.name, check.vel, got, check.want)
		}
	}
	if !RecordedDynamics([]Zone{{Gain: 0}, {Gain: .7}, {Gain: .7 * (1 + 5e-7)}}) || RecordedDynamics([]Zone{{Gain: 1}, {Gain: .999}}) {
		t.Error("RecordedDynamics does not apply the common-gain rule")
	}
}
