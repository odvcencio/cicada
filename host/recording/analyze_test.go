package recording

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) Audio {
	t.Helper()
	b, err := os.ReadFile("testdata/pencil-taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	a, err := DecodeWAV(b)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFifteenTapsToPinnedPack(t *testing.T) {
	start := time.Now()
	a := fixture(t)
	options := DefaultOptions()
	options.AutoPitch = false
	hits, err := Analyze([]Audio{a}, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 15 {
		t.Fatalf("detected %d taps; want 15", len(hits))
	}
	for i, h := range hits {
		want := int((.1 + float64(i)*.22) * float64(a.Rate))
		if math.Abs(float64(h.Start-want)) > float64(a.Rate)*.008 {
			t.Errorf("tap %d start %d want near %d", i, h.Start, want)
		}
		if len(h.PCM) >= int(.2*float64(a.Rate)) || h.PCM[0] != 0 || h.PCM[len(h.PCM)-1] != 0 {
			t.Errorf("tap %d trimming/fades", i)
		}
		peak := 0.0
		for _, x := range h.PCM {
			peak = max(peak, math.Abs(float64(x)))
		}
		if peak < .85 || peak > .901 {
			t.Errorf("normalized peak %f", peak)
		}
	}
	p, err := Build("pencil", hits, 3)
	if err != nil {
		t.Fatal(err)
	}
	if p.Pin != Digest(p.Files["manifest.json"]) || len(p.Manifest.Zones) != 15 {
		t.Fatal("pack pin/map")
	}
	counts := map[int]int{}
	for _, z := range p.Manifest.Zones {
		counts[z.Layer]++
		if z.Count != 5 || z.Gain <= 0 || z.Gain > 1 || !z.OneShot {
			t.Fatal("round robin/layer gain", z)
		}
	}
	if len(counts) != 3 {
		t.Fatal("velocity layers", counts)
	}
	for _, asset := range p.Manifest.Assets {
		if asset.License != "owner recording" || asset.SourceSHA256 != a.SHA256 {
			t.Fatal("provenance")
		}
		g, err := gzip.NewReader(bytes.NewReader(p.Files[asset.Path]))
		if err != nil {
			t.Fatal(err)
		}
		wav, err := io.ReadAll(g)
		g.Close()
		if err != nil {
			t.Fatal(err)
		}
		if Digest(wav) != asset.WAVSHA256 || Digest(p.Files[asset.Path]) != asset.SHA256 {
			t.Fatal("sample checksum")
		}
		if _, err := DecodeWAV(wav); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := p.Write(dir); err != nil {
		t.Fatal("idempotent publication", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hit_001.wav.gz"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if p.Write(dir) == nil {
		t.Fatal("overwrote existing corrupt pack")
	}
	p2, err := Build("pencil", hits, 3)
	if err != nil || p2.Pin != p.Pin {
		t.Fatal("nondeterministic pack")
	}
	t.Logf("15 fixture taps → 3 velocity layers × 5 round robins in %s", time.Since(start))
}

func TestPitchAndNotes(t *testing.T) {
	for _, rate := range []int{16000, 44100, 48000, 96000} {
		for _, frequency := range []float64{110, 440, 880} {
			pcm := make([]float32, rate/3)
			for i := rate / 100; i < len(pcm); i++ {
				x := float64(i-rate/100) / float64(rate)
				pcm[i] = float32(.6 * math.Exp(-x*15) * (math.Sin(2*math.Pi*frequency*x) + .15*math.Sin(4*math.Pi*frequency*x)))
			}
			a, err := DecodeWAV(EncodeWAV(pcm, rate))
			if err != nil {
				t.Fatal(err)
			}
			h, err := Analyze([]Audio{a}, DefaultOptions())
			if err != nil || len(h) != 1 {
				t.Fatalf("%dHz %.0f: detected %d hits: %v", rate, frequency, len(h), err)
			}
			cents := 1200 * math.Log2(h[0].PitchHz/frequency)
			if math.Abs(cents) > 15 || h[0].Confidence < .8 {
				t.Fatalf("rate %d frequency %.0f pitch %.1f cents %.1f confidence %.2f", rate, frequency, h[0].PitchHz, cents, h[0].Confidence)
			}
		}
	}
}

func TestRejectInvalidAndSilentRecordings(t *testing.T) {
	if _, err := DecodeWAV([]byte("bad")); err == nil {
		t.Fatal("invalid WAV accepted")
	}
	a := Audio{Rate: 48000, PCM: make([]float32, 4800)}
	if _, err := Analyze([]Audio{a}, DefaultOptions()); err == nil {
		t.Fatal("silence accepted")
	}
	a.PCM[0] = float32(math.NaN())
	if _, err := Analyze([]Audio{a}, DefaultOptions()); err == nil {
		t.Fatal("NaN accepted")
	}
	if _, err := DecodeWAV(EncodeWAV(a.PCM, a.Rate)); err == nil {
		t.Fatal("NaN WAV accepted")
	}
	if _, err := Build("../escape", nil, 3); err == nil {
		t.Fatal("unsafe name accepted")
	}
}

func TestManifestWireFormat(t *testing.T) {
	h, err := Analyze([]Audio{fixture(t)}, Options{Root: 60, Layers: 3})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Build("pencil", h, 3)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(p.Files["manifest.json"], &wire); err != nil {
		t.Fatal(err)
	}
	if wire["format"] != "cicada.instrument-pack/1" {
		t.Fatal("wire format")
	}
	config := wire["config"].(map[string]any)
	if config["Humanize"].(map[string]any)["Seed"] != "0" {
		t.Fatal("exact seed")
	}
}
