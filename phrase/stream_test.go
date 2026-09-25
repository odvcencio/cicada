package phrase

import (
	"math"
	"reflect"
	"testing"
)

func TestPCG32FixedVector(t *testing.T) {
	s := newStream(4242)
	want := [...]uint32{744572222, 3209802928, 3019347011, 1444383813, 3602554767, 1236057910, 546096739, 2432106775}
	for index, expected := range want {
		if got := s.next(); got != expected {
			t.Fatalf("draw %d: got %d, want %d", index, got, expected)
		}
	}
}

func TestChooseOneStillConsumesADraw(t *testing.T) {
	s := newStream(4242)
	if got := s.choose("rhythm", 3, 1); got != 0 {
		t.Fatalf("choose(1) = %d", got)
	}
	if len(s.trace) != 1 || s.trace[0] != (Draw{"rhythm", 3, 744572222, 0}) {
		t.Fatalf("unexpected draw trace: %+v", s.trace)
	}
	if got := s.next(); got != 3209802928 {
		t.Fatalf("choose(1) did not advance the stream: %d", got)
	}
}

func TestDensityAndDefaults(t *testing.T) {
	got, err := normalize(Params{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps != 16 || got.RootOctave != 2 || got.Structure != AABA || got.density != 0 {
		t.Fatalf("zero-value defaults or density changed: %+v", got)
	}
	got, err = normalize(DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if got.density != 38 || got.accentDensity != 32 || got.slideDensity != 26 || got.octaveJump != 19 {
		t.Fatalf("default density quantization changed: %+v", got)
	}
	for _, test := range []struct {
		value float32
		want  uint8
	}{
		{0, 0}, {1.0 / 128, 1}, {3.0 / 128, 2}, {1, 64},
	} {
		value, err := quantizeDensity(test.value)
		if err != nil || value != test.want {
			t.Fatalf("quantizeDensity(%v) = %d, %v; want %d", test.value, value, err, test.want)
		}
	}
	for _, value := range []float32{-0.1, 1.1, float32(math.NaN()), float32(math.Inf(1))} {
		if _, err := quantizeDensity(value); err == nil {
			t.Fatalf("invalid density %v was accepted", value)
		}
	}
}

func TestRhythmTraceIsRepeatable(t *testing.T) {
	first := newStream(4242)
	second := newStream(4242)
	a := generateRhythm(&first, 16, 38, false)
	b := generateRhythm(&second, 16, 38, false)
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(first.trace, second.trace) {
		t.Fatal("same seed produced different rhythm or trace")
	}
	if len(first.trace) == 0 || first.trace[0].Pass != "rhythm" || first.trace[0].Raw != 744572222 {
		t.Fatalf("missing first rhythm draw: %+v", first.trace)
	}
}
