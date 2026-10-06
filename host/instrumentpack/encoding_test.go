package instrumentpack

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/audioencoding"
)

func encodedFixture(t *testing.T, name string, scale float32) (Manifest, []byte, []byte) {
	t.Helper()
	m, _, _ := fixturePack(t)
	b, err := os.ReadFile(filepath.Join("..", "audioencoding", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	a := &m.Assets[0]
	a.Path, a.Encoding, a.Frames = "samples/"+name, "flac", 896
	a.WAVBytes, a.WAVSHA256, a.Scale = 0, "", scale
	a.Bytes, a.SHA256 = int64(len(b)), digest(b)
	values := []float32{0, 1.0 / 32768, -1.0 / 32768, .25, -.5, 32767.0 / 32768, -1}
	if name == "quiet.wav.gz" {
		a.Encoding, a.Frames = "wav-gzip", 640
		g, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		wav, err := io.ReadAll(g)
		if err != nil {
			t.Fatal(err)
		}
		g.Close()
		a.WAVBytes, a.WAVSHA256 = int64(len(wav)), digest(wav)
		values = []float32{0, 1.0 / (1 << 20), -1.0 / (1 << 25), 1.0 / (1 << 30), -1.0 / (1 << 38)}
	}
	pcm := [][]float32{make([]float32, a.Frames)}
	for i := range pcm[0] {
		pcm[0][i] = values[i%len(values)] * scale
	}
	a.PCMHash = audioencoding.PCMHash(pcm)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return m, data, b
}

func TestEncodedPacksPinPCMAndRenderWithoutAllocations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scale float32
	}{{"integer.flac", 1}, {"integer.flac", 1.0 / (1 << 20)}, {"quiet.wav.gz", 1}} {
		t.Run(fmt.Sprintf("%s-%g", tc.name, tc.scale), func(t *testing.T) {
			m, data, encoded := encodedFixture(t, tc.name, tc.scale)
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "samples"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, m.Assets[0].Path), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			p, err := Load(dir, "manifest.json", digest(data))
			if err != nil {
				t.Fatal(err)
			}
			voice, err := p.New(48000)
			if err != nil {
				t.Fatal(err)
			}
			if n := testing.AllocsPerRun(20, func() {
				voice.Reset()
				voice.NoteOn(60, 100)
				for i := 0; i < 2048; i++ {
					voice.NextStereo()
				}
			}); n != 0 {
				t.Fatalf("render allocations: %g", n)
			}
			bad := m.Assets[0]
			bad.PCMHash = digest([]byte("different PCM"))
			if _, err := DecodeAsset(bad, encoded); err == nil {
				t.Fatal("incorrect PCM pin admitted")
			}
			if m.Assets[0].Encoding == "wav-gzip" {
				bad = m.Assets[0]
				bad.WAVSHA256 = digest([]byte("different WAV"))
				if _, err := DecodeAsset(bad, encoded); err == nil {
					t.Fatal("incorrect WAV pin admitted")
				}
			}
			bad = m.Assets[0]
			bad.Frames++
			if _, err := DecodeAsset(bad, encoded); err == nil {
				t.Fatal("incorrect dimensions admitted")
			}
		})
	}
}

func TestEncodedManifestRejectsInvalidDescriptors(t *testing.T) {
	for _, mutate := range []func(*Asset){func(a *Asset) { a.Scale = .3 }, func(a *Asset) { a.Encoding = "opus" }, func(a *Asset) { a.Path = "samples/x.wav.gz" }, func(a *Asset) { a.PCMHash = "" }, func(a *Asset) { a.Encoding = "" }, func(a *Asset) { a.WAVBytes = 100 }} {
		m, _, _ := encodedFixture(t, "integer.flac", 1)
		mutate(&m.Assets[0])
		b, _ := json.Marshal(m)
		if _, err := DecodeManifest(b); err == nil {
			t.Fatal("invalid encoded manifest admitted")
		}
	}
}
