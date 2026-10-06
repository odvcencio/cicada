package audioencoding

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func fixture(t *testing.T, name string) (Asset, []byte, [][]float32) {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	a := Asset{Encoding: "flac", Rate: 48000, Channels: 1, Frames: 896, Scale: 1, Bytes: int64(len(b)), SHA256: digest(b)}
	p := [][]float32{make([]float32, a.Frames)}
	values := []int16{0, 1, -1, 8192, -16384, 32767, -32768}
	for i := range p[0] {
		p[0][i] = float32(values[i%len(values)]) / 32768
	}
	if name == "quiet.wav.gz" {
		a.Encoding = "wav-gzip"
		a.Frames = 640
		g, _ := gzip.NewReader(bytes.NewReader(b))
		var wave bytes.Buffer
		wave.ReadFrom(g)
		g.Close()
		a.DecodedBytes = int64(wave.Len())
		p = [][]float32{make([]float32, a.Frames)}
		v := []float32{0, 1.0 / (1 << 20), -1.0 / (1 << 25), 1.0 / (1 << 30), -1.0 / (1 << 38)}
		for i := range p[0] {
			p[0][i] = v[i%len(v)]
		}
	}
	a.PCMHash = PCMHash(p)
	return a, b, p
}
func TestDecodeExactTiers(t *testing.T) {
	for _, name := range []string{"integer.flac", "quiet.wav.gz"} {
		t.Run(name, func(t *testing.T) {
			a, b, want := fixture(t, name)
			got, e := Decode(a, b)
			if e != nil {
				t.Fatal(e)
			}
			for c := range want {
				for i := range want[c] {
					if math.Float32bits(got[c][i]) != math.Float32bits(want[c][i]) {
						t.Fatalf("PCM mismatch at %d/%d", c, i)
					}
				}
			}
		})
	}
}
func TestScaledPCMHash(t *testing.T) {
	a, b, p := fixture(t, "integer.flac")
	a.Scale = 1.0 / (1 << 20)
	for i := range p[0] {
		p[0][i] *= a.Scale
	}
	a.PCMHash = PCMHash(p)
	got, e := Decode(a, b)
	if e != nil {
		t.Fatal(e)
	}
	if PCMHash(got) != PCMHash(p) {
		t.Fatal("scaled PCM mismatch")
	}
}
func TestAdmissionRejectsPinsDimensionsAndBounds(t *testing.T) {
	a, b, _ := fixture(t, "integer.flac")
	for name, change := range map[string]func(*Asset){"hash": func(a *Asset) { a.SHA256 = string(make([]byte, 64)) }, "size": func(a *Asset) { a.Bytes++ }, "pcm hash": func(a *Asset) { a.PCMHash = digest(nil) }, "frames": func(a *Asset) { a.Frames++ }, "rate": func(a *Asset) { a.Rate = 44100 }, "channels": func(a *Asset) { a.Channels = 2 }, "frame budget": func(a *Asset) { a.Frames = 8<<20 + 1 }, "scale": func(a *Asset) { a.Scale = .3 }} {
		t.Run(name, func(t *testing.T) {
			q := a
			change(&q)
			if _, e := Decode(q, b); e == nil {
				t.Fatal("invalid asset admitted")
			}
		})
	}
	corrupt := append([]byte(nil), b...)
	corrupt[len(corrupt)-1] ^= 1
	a.SHA256 = digest(corrupt)
	if _, e := Decode(a, corrupt); e == nil {
		t.Fatal("bad FLAC CRC admitted")
	}
}
func TestGzipRejectsExpansionTrailingDataAndNonfinite(t *testing.T) {
	a, b, _ := fixture(t, "quiet.wav.gz")
	q := a
	q.DecodedBytes--
	if _, e := Decode(q, b); e == nil {
		t.Fatal("expansion admitted")
	}
	trailing := append(append([]byte(nil), b...), b...)
	q = a
	q.Bytes = int64(len(trailing))
	q.SHA256 = digest(trailing)
	if _, e := Decode(q, trailing); e == nil {
		t.Fatal("trailing stream admitted")
	}
	g, _ := gzip.NewReader(bytes.NewReader(b))
	var raw bytes.Buffer
	raw.ReadFrom(g)
	g.Close()
	wave := raw.Bytes()
	pcm, _, e := DecodeWAV(wave)
	if e != nil {
		t.Fatal(e)
	}
	if len(pcm[0]) != a.Frames {
		t.Fatal("fixture shape")
	} // The fixture has a fact chunk; locate its data chunk rather than assuming offset 44.
	for o := 12; o < len(wave); {
		n := int(binary.LittleEndian.Uint32(wave[o+4:]))
		if string(wave[o:o+4]) == "data" {
			binary.LittleEndian.PutUint32(wave[o+8:], math.Float32bits(float32(math.NaN())))
			break
		}
		o += 8 + n + (n & 1)
	}
	if _, _, e := DecodeWAV(wave); e == nil {
		t.Fatal("nonfinite source admitted")
	}
}
func TestPowerOfTwoValidation(t *testing.T) {
	for _, v := range []float32{1, .5, 1.0 / (1 << 38), 2} {
		if !PowerOfTwo(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []float32{0, -1, .3, float32(math.Inf(1)), float32(math.NaN()), math.SmallestNonzeroFloat32} {
		if PowerOfTwo(v) {
			t.Fatal(v)
		}
	}
}
