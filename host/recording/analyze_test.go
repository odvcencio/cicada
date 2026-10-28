package recording

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) Audio {
	t.Helper()
	b, err := os.ReadFile("testdata/taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	if Digest(b) != "2e05dd4b28e1a8c4674e502590483466918068779d047775b6c9708c1598b0d8" {
		t.Fatal("synthetic taps fixture checksum changed")
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
	p, err := Build("recorded", hits, 3)
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
		if asset.License != "user recording" || asset.SourceSHA256 != a.SHA256 {
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
	p2, err := Build("recorded", hits, 3)
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
			options := DefaultOptions()
			options.AutoPitch = true
			h, err := Analyze([]Audio{a}, options)
			if err != nil || len(h) != 1 {
				t.Fatalf("%dHz %.0f: detected %d hits: %v", rate, frequency, len(h), err)
			}
			cents := 1200 * math.Log2(h[0].PitchHz/frequency)
			if math.Abs(cents) > 15 || h[0].Confidence < .8 {
				t.Fatalf("rate %d frequency %.0f pitch %.1f cents %.1f confidence %.2f", rate, frequency, h[0].PitchHz, cents, h[0].Confidence)
			}
			if want := int(math.Round(69 + 12*math.Log2(frequency/440))); h[0].Root != want {
				t.Fatal("pitched root", h[0].Root, want)
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
	p, err := Build("recorded", h, 3)
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

// A pack folder is reused only when every generated file is identical, so the
// numbers of an analysis may not depend on the machine. Go lets a compiler fuse
// x*y+z into one instruction with another last digit, and arm64 and GOAMD64=v3
// builds do; analyze.go forbids it with explicit conversions. The digest below
// covers what a fused sum of products would change in the output: the pitch and
// confidence that come from autocorrelation sums, the hit boundaries and peaks,
// and every sample of every hit, for tapped, pitched and ringing recordings. It
// was taken on a build that does not fuse. Run the test with GOAMD64=v3 to see
// that a fusing build gives the same digest, and with a conversion removed from
// estimatePitch to see that it then does not. The energy sums can differ only
// in digits far below what any decision reads, so no digest can see them.
//
// The recordings come from integer arithmetic alone, and the digest leaves out
// logarithms: the Go math library may end a result in another last digit on
// another processor, and the digest must not depend on that.
func TestAnalysisDoesNotDependOnFusedMultiplyAdd(t *testing.T) {
	sum := sha256.New()
	add := func(values ...float64) {
		for _, v := range values {
			binary.Write(sum, binary.LittleEndian, math.Float64bits(v))
		}
	}
	addHits := func(hits []Hit) {
		add(float64(len(hits)))
		for _, h := range hits {
			add(float64(h.Input), float64(h.Start), float64(h.End), h.Peak, h.PitchHz, h.Confidence, float64(h.Root))
			if h.Cut {
				add(1)
			}
			for _, x := range h.PCM {
				add(float64(x))
			}
		}
	}
	taps, err := Analyze([]Audio{fixture(t)}, Options{Root: 60, Layers: 3})
	if err != nil {
		t.Fatal(err)
	}
	addHits(taps)
	pitched, err := Analyze([]Audio{{PCM: integerNote(), Rate: 48000}}, Options{Root: 60, Layers: 3, AutoPitch: true})
	if err != nil {
		t.Fatal(err)
	}
	addHits(pitched)
	cut, reports, err := AnalyzeTakes([]Audio{{PCM: integerCrashes(), Rate: 48000}}, Options{Root: 49, Layers: 1, Ringing: true})
	if err != nil {
		t.Fatal(err)
	}
	addHits(cut)
	add(float64(reports[0].Merged))
	const want = "eeff2a371ab67ac15fd87e1cc3d701661d8f3a33ce910b39612d430933e69a8f"
	if got := fmt.Sprintf("%x", sum.Sum(nil)); got != want {
		t.Fatalf("analysis digest %s, want %s: the numbers of an analysis changed; if the change is meant, take the new digest from a build that does not fuse", got, want)
	}
}

// xorshift is an integer generator, so a recording made with it is the same on
// every processor.
type xorshift uint64

// next returns a signed 24-bit sample.
func (s *xorshift) next() int64 {
	x := uint64(*s)
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	*s = xorshift(x)
	return int64(x>>40) - 1<<23
}

// integerNote is a triangle wave at 200 Hz that starts 10 ms into a third of a
// second and fades out in a straight line over a sixth of a second, then stops.
func integerNote() []float32 {
	const period, length = 240, 48000 / 6
	pcm := make([]float32, 48000/3)
	for i := 0; i < length; i++ {
		phase := i % period
		tri := phase - period/4 // a triangle between -60 and 60
		if phase >= period/2 {
			tri = 3*period/4 - phase
		}
		pcm[480+i] = float32(tri*(length-i)) / 5e5
	}
	return pcm
}

// integerCrashes is three cymbal strikes four seconds apart in 10.5 s, each
// swelling over 12 ms and fading with a time constant of 2 s, so that every
// tail runs on under the next strike, over a faint room.
func integerCrashes() []float32 {
	const rate, rise = 48000, 48000 * 12 / 1000
	pcm := make([]int64, rate*21/2)
	room, noise := xorshift(0x5eed), xorshift(0xc0ffee)
	for i := range pcm {
		pcm[i] = room.next() / 4096
	}
	for k, level := range []int64{550, 500, 600} {
		at := rate/2 + k*4*rate
		decay := int64(1) << 30
		for i := 0; at+i < len(pcm); i++ {
			swell := decay * int64(min(i, rise)) / rise
			pcm[at+i] += (noise.next() * swell >> 30) * level / 1000
			decay -= decay / (2 * rate)
		}
	}
	out := make([]float32, len(pcm))
	for i, v := range pcm {
		out[i] = float32(v) / (1 << 23)
	}
	return out
}
