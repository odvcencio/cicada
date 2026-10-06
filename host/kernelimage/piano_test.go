package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
)

func TestPianoCapabilityImage(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 8, BPMMilli: 120_000}
	cfg.Track[0].Kind, cfg.Track[0].PianoSustain = engine.VoicePiano, .75
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 13 || binary.LittleEndian.Uint16(data[30:32]) != kernelimage.PianoCapability {
		t.Fatal("piano image version or capability changed")
	}
	decoded, err := kernelimage.Decode(data, 48_000, 128)
	if err != nil || !reflect.DeepEqual(cfg.Track, decoded.Track) {
		t.Fatalf("piano sustain image roundtrip: %v", err)
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatal(err)
	}
	pedalBits := make([]byte, 4)
	binary.LittleEndian.PutUint32(pedalBits, math.Float32bits(.75))
	pedalOffset := bytes.Index(data[32:], pedalBits) + 32
	if pedalOffset < 32 {
		t.Fatal("piano sustain field missing from image")
	}
	for _, value := range []float32{-1, 2, float32(math.NaN()), float32(math.Inf(1))} {
		binary.LittleEndian.PutUint32(data[pedalOffset:], math.Float32bits(value))
		if _, err := kernelimage.Decode(data, 48_000, 128); err == nil {
			t.Fatalf("image decoded invalid sustain %g", value)
		}
	}
	binary.LittleEndian.PutUint32(data[pedalOffset:], math.Float32bits(.75))
	for _, capability := range []uint16{0, kernelimage.DelayCapability, 1 << 15} {
		binary.LittleEndian.PutUint16(data[30:32], capability)
		if _, err := kernelimage.Decode(data, 48_000, 128); err == nil {
			t.Fatalf("piano image accepted unsupported capability %d", capability)
		}
	}
	for _, value := range []float32{-1, 2, float32(math.NaN()), float32(math.Inf(1))} {
		cfg.Track[0].PianoSustain = value
		if _, err := kernelimage.Encode(cfg); err == nil {
			t.Fatalf("image encoded invalid sustain %g", value)
		}
	}
}
