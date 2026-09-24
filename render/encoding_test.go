package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestDitherPCGFixedStreamAndSilenceVector(t *testing.T) {
	rng := newPCG32(4242)
	want := []uint32{744572222, 3209802928, 3019347011, 1444383813, 3602554767, 1236057910, 546096739, 2432106775}
	for i, expected := range want {
		if got := rng.next(); got != expected {
			t.Fatalf("PCG draw %d: got %d want %d", i, got, expected)
		}
	}
	encoder, err := newWAVEncoder(16, true, 4242, 1)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	var output [8 * 4]byte
	for frame := 0; frame < 8; frame++ {
		encoder.writeFrame(output[frame*4:], 0, 0, 1, &report)
	}
	expected := []byte{
		0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0,
		1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	if !bytes.Equal(output[:], expected) {
		t.Fatalf("silent dither vector differs: %x", output)
	}
}

func TestWAVEncoderFormatsAndNearestEven(t *testing.T) {
	noDither, err := newWAVEncoder(16, false, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	var pcm16 [4]byte
	noDither.writeFrame(pcm16[:], .5, -.5, 1, &report)
	if left, right := int16(binary.LittleEndian.Uint16(pcm16[:2])), int16(binary.LittleEndian.Uint16(pcm16[2:])); left != 16384 || right != -16384 {
		t.Fatalf("nearest-even PCM16: %d %d", left, right)
	}
	noDither.writeFrame(pcm16[:], 0, 0, 1, &report)
	if pcm16 != [4]byte{} {
		t.Fatalf("disabled dither changed silence: %x", pcm16)
	}
	float, err := newWAVEncoder(32, true, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wavFloat [8]byte
	float.writeFrame(wavFloat[:], math.Float32frombits(0x80000000), .5, 1, &report)
	if binary.LittleEndian.Uint32(wavFloat[:4]) != 0 || math.Float32frombits(binary.LittleEndian.Uint32(wavFloat[4:])) != .5 {
		t.Fatalf("float WAV sample or signed zero: %x", wavFloat)
	}
}
