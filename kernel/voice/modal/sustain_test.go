package modal

import (
	"math"
	"testing"
)

func TestSustainValidatesAndUsesTheMIDIThreshold(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	for _, value := range []float32{-.01, 1.01, float32(math.NaN()), float32(math.Inf(1))} {
		if v.SetSustain(value) == nil {
			t.Fatalf("accepted sustain %g", value)
		}
	}
	for _, tc := range []struct {
		value float32
		down  bool
	}{{0, false}, {.49, false}, {.5, true}, {1, true}} {
		if err := v.SetSustain(tc.value); err != nil || v.pedal != tc.down {
			t.Fatalf("sustain %g: pedal %v, want %v (%v)", tc.value, v.pedal, tc.down, err)
		}
	}
}

func TestPedalKeepsBarsRingingAfterNoteOffAndLiftDampsThem(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	if err := v.SetSustain(1); err != nil {
		t.Fatal(err)
	}
	v.NoteOn(60, 100, false)
	render(v, 4800)
	// A bar that never got a key-up is the reference for "still ringing".
	free := *v
	v.NoteOff()
	for n := 0; n < 48000; n++ {
		if got, want := v.Next(), free.Next(); got != want {
			t.Fatalf("frame %d after the key-up: %g, want the undamped %g", n, got, want)
		}
	}
	ringing := rms(render(v, 960))
	render(&free, 960)
	t.Logf("METRIC modal profile=vibraphone pedal_down_rms_1s_after_key_up=%.6f", ringing)
	if ringing < 1e-3 {
		t.Fatalf("pedal down left %.6f after the key-up", ringing)
	}
	// Lifting the pedal does what the key-up did before the pedal existed.
	damped := free
	damped.pedal = false
	damped.NoteOff()
	if err := v.SetSustain(0); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 9600; n++ {
		if got, want := v.Next(), damped.Next(); got != want {
			t.Fatalf("frame %d after the lift: %g, want %g from an ordinary key-up", n, got, want)
		}
	}
	render(v, 4800)
	if tail := rms(render(v, 960)); tail > ringing*1e-3 {
		t.Fatalf("lift left %.6f, from %.6f", tail, ringing)
	}
}

func TestPedalLiftSparesBarsWhoseKeysAreDown(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	v.NoteOn(60, 100, false)
	render(v, 4800)
	if err := v.SetSustain(1); err != nil {
		t.Fatal(err)
	}
	free := *v
	if err := v.SetSustain(0); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4800; n++ {
		if v.Next() != free.Next() {
			t.Fatalf("lifting the pedal damped a bar whose key is down (frame %d)", n)
		}
	}
	v.NoteOff()
	render(v, 4800)
	if tail := rms(render(v, 960)); tail > 1e-5 {
		t.Fatalf("the key-up after the lift left %.6f", tail)
	}
}

func TestPedalLiftDampsOnlyTheReleasedBar(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	v.SetSustain(1)
	v.NoteOn(60, 100, false)
	v.NoteOff()
	v.NoteOn(64, 100, false)
	released, held := &v.strikes[0], &v.strikes[1]
	radius := held.modes[0].radius
	render(v, 4800)
	v.SetSustain(0)
	v.Next()
	if got := released.modes[0].radius; got != v.damper {
		t.Fatalf("released bar radius %g, want the damped %g", got, v.damper)
	}
	if held.modes[0].radius != radius {
		t.Fatalf("held bar was damped: radius %g, want %g", held.modes[0].radius, radius)
	}
}

func TestDamperFollowsKeyAndPedalStateNotTheirOrder(t *testing.T) {
	// A key-up and a pedal change in one sample give one result in either
	// order, because a damper falls when the key is up and the pedal is up.
	start := func(pedal float32) *Voice {
		v := newTestVoice(t, Vibraphone, 48000)
		v.SetSustain(pedal)
		v.NoteOn(60, 100, false)
		render(v, 4800)
		return v
	}
	same := func(name string, a, b *Voice) {
		t.Helper()
		for n := 0; n < 9600; n++ {
			if x, y := a.Next(), b.Next(); x != y {
				t.Fatalf("%s: frame %d is %g in one order and %g in the other", name, n, x, y)
			}
		}
	}
	pressFirst, offFirst := start(0), start(0)
	pressFirst.SetSustain(1)
	pressFirst.NoteOff()
	offFirst.NoteOff()
	offFirst.SetSustain(1)
	same("press and key-up", pressFirst, offFirst)
	if tail := rms(render(pressFirst, 960)); tail < 1e-3 {
		t.Fatalf("a press in the sample of the key-up left only %.6f", tail)
	}
	liftFirst, offLast := start(1), start(1)
	liftFirst.SetSustain(0)
	liftFirst.NoteOff()
	offLast.NoteOff()
	offLast.SetSustain(0)
	same("lift and key-up", liftFirst, offLast)
	render(liftFirst, 4800)
	if tail := rms(render(liftFirst, 960)); tail > 1e-5 {
		t.Fatalf("a lift in the sample of the key-up left %.6f", tail)
	}
}

func TestStrikeOnRingingBarBehavesAsWithoutAPedal(t *testing.T) {
	// With the pedal down a key-up is silent, so a bar struck again while it
	// rings takes a fresh strike, as it does when no key-up arrives.
	pedal, plain := newTestVoice(t, Vibraphone, 48000), newTestVoice(t, Vibraphone, 48000)
	pedal.SetSustain(1)
	pedal.NoteOn(60, 100, false)
	plain.NoteOn(60, 100, false)
	render(pedal, 2400)
	render(plain, 2400)
	pedal.NoteOff()
	pedal.NoteOn(60, 100, false)
	plain.NoteOn(60, 100, false)
	if pedal.ActiveVoices() != 2 || plain.ActiveVoices() != 2 {
		t.Fatalf("strikes: %d with the pedal, %d without a key-up", pedal.ActiveVoices(), plain.ActiveVoices())
	}
	for n := 0; n < 24000; n++ {
		if got, want := pedal.Next(), plain.Next(); got != want {
			t.Fatalf("frame %d: %g, want %g", n, got, want)
		}
	}
}

func TestSlideKeepsTheRetunedBarHeld(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	v.SetSustain(1)
	v.NoteOn(48, 100, false)
	v.NoteOff()
	v.NoteOn(60, 80, true)
	render(v, 480)
	radius := v.strikes[v.latest].modes[0].radius
	v.SetSustain(0)
	v.Next()
	if v.strikes[v.latest].modes[0].radius != radius {
		t.Fatal("pedal lift damped the bar of a note that is still down")
	}
}

func TestSlideAfterKeyUpFindsTheBarDamped(t *testing.T) {
	// Without a pedal the key-up damps at once, so a slide in the same sample
	// retunes a bar that is already dying. The damper must not wait for the
	// next sample, which would let the slide hold the bar.
	ring := func(pedal float32) float64 {
		v := newTestVoice(t, Vibraphone, 48000)
		v.SetSustain(pedal)
		v.NoteOn(48, 100, false)
		render(v, 4800)
		v.NoteOff()
		v.NoteOn(60, 80, true)
		render(v, 7200)
		return rms(render(v, 960))
	}
	damped, held := ring(0), ring(1)
	if held < 1e-3 || damped > held*1e-3 {
		t.Fatalf("slide after a key-up: %.6f without the pedal, %.6f with it", damped, held)
	}
}

func TestPedalChangesNothingForBarsWithoutDampers(t *testing.T) {
	for p := Profile(0); p < ProfileCount; p++ {
		if p == Vibraphone {
			continue
		}
		pedal, plain := newTestVoice(t, p, 48000), newTestVoice(t, p, 48000)
		pedal.SetSustain(1)
		for _, v := range []*Voice{pedal, plain} {
			v.NoteOn(60, 100, false)
		}
		render(pedal, 2400)
		render(plain, 2400)
		pedal.NoteOff()
		plain.NoteOff()
		for n := 0; n < 4800; n++ {
			if pedal.Next() != plain.Next() {
				t.Fatalf("%s: the pedal changed the output", Names[p])
			}
		}
		pedal.SetSustain(0)
		for n := 0; n < 4800; n++ {
			if pedal.Next() != plain.Next() {
				t.Fatalf("%s: lifting the pedal changed the output", Names[p])
			}
		}
	}
}

func TestResetReleasesThePedal(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	v.SetSustain(1)
	v.Reset()
	v.NoteOn(60, 100, false)
	render(v, 2400)
	v.NoteOff()
	render(v, 4800)
	if tail := rms(render(v, 960)); tail > 1e-5 {
		t.Fatalf("a reset voice kept the pedal: %.6f after the key-up", tail)
	}
}

func TestPedalKeepsStrikesBoundedAndAllocationFree(t *testing.T) {
	v := newTestVoice(t, Vibraphone, 48000)
	v.SetSustain(1)
	for hit := 0; hit < 20; hit++ {
		v.NoteOn(uint8(48+hit), 110, false)
		v.NoteOff()
		if v.ActiveVoices() > MaxVoices {
			t.Fatal("voice count exceeds its fixed bound")
		}
		for n := 0; n < 128; n++ {
			if x := float64(v.Next()); math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 4 {
				t.Fatalf("invalid output %g", x)
			}
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		v.Reset()
		v.SetSustain(1)
		for hit := 0; hit < MaxVoices+1; hit++ {
			v.NoteOn(uint8(60+hit), 96, false)
			v.NoteOff()
		}
		for n := 0; n < 128; n++ {
			v.Next()
		}
		v.SetSustain(0)
	})
	if allocs != 0 {
		t.Fatalf("pedal calls allocated %.2f times", allocs)
	}
}
