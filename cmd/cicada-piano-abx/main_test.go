package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/voice/sample"
)

func TestBlindTrialsAreReproducibleAndUnbiased(t *testing.T) {
	a, b := rand.New(rand.NewPCG(42, 7)), rand.New(rand.NewPCG(42, 7))
	var counts [2][2]int
	for i := 0; i < 10000; i++ {
		trial, key, files := blindTrial(a, "trial", "clip", 1, []byte("model"), []byte("sample"))
		two, keyTwo, filesTwo := blindTrial(b, "trial", "clip", 1, []byte("model"), []byte("sample"))
		if !reflect.DeepEqual(trial, two) || !reflect.DeepEqual(key, keyTwo) || !reflect.DeepEqual(files, filesTwo) {
			t.Fatal("same seed changed a trial")
		}
		if key.A == key.B {
			t.Fatal("references share an identity")
		}
		xIndex := 0
		if key.X == "B" {
			xIndex = 1
		}
		if !bytes.Equal(files[2].data, files[xIndex].data) {
			t.Fatal("X differs from its keyed reference")
		}
		aIndex := 0
		if key.A == "sampled" {
			aIndex = 1
		}
		counts[aIndex][xIndex]++
	}
	for _, row := range counts {
		for _, count := range row {
			if count < 2300 || count > 2700 {
				t.Fatalf("reference and X choices are biased or coupled: %v", counts)
			}
		}
	}
}

func TestMatchPairMeasuresEncodedAudioAndPreservesHeadroom(t *testing.T) {
	a, b := audio{make([]float32, outputRate*2), make([]float32, outputRate*2)}, audio{make([]float32, outputRate*2), make([]float32, outputRate*2)}
	for i := range a.left {
		phase := 2 * math.Pi * float64(i) / outputRate
		a.left[i], a.right[i] = float32(.6*math.Sin(220*phase)), float32(.4*math.Sin(220*phase))
		b.left[i], b.right[i] = float32(.04*math.Sin(880*phase)), float32(.03*math.Sin(880*phase))
	}
	x, y, mx, my, err := matchPair(a, b, -12)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(mx.LUFS-my.LUFS) > .1 || mx.TruePeak > -1 || my.TruePeak > -1 {
		t.Fatalf("matching failed: %+v %+v", mx, my)
	}
	for _, output := range []audio{x, y} {
		wav, err := encodeWAV(output)
		if err != nil {
			t.Fatal(err)
		}
		r, err := decodeWAV(wav)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.Left, output.left) || !reflect.DeepEqual(r.Right, output.right) {
			t.Fatal("loudness measurement differs from delivered PCM24")
		}
	}
	if _, _, _, _, err := matchPair(audio{make([]float32, 48000), make([]float32, 48000)}, b, -23); err == nil {
		t.Fatal("matched silent reference")
	}
}

func TestPerformancesHaveValidEventsAndFitEightNotes(t *testing.T) {
	fixtures := performances()
	if len(fixtures) != 7 {
		t.Fatalf("performances=%d", len(fixtures))
	}
	for _, f := range fixtures {
		held, pending := map[uint8]bool{}, map[uint8]bool{}
		pedal, last := false, -1
		for _, e := range f.Events {
			if e.Frame < last || e.Frame < 0 || e.Frame >= f.Frames {
				t.Fatalf("%s has invalid event frame", f.ID)
			}
			last = e.Frame
			switch e.Kind {
			case "on":
				if held[e.Note] || e.Velocity < 1 || e.Velocity > 127 {
					t.Fatalf("%s invalid note-on", f.ID)
				}
				held[e.Note] = true
			case "off":
				if !held[e.Note] {
					t.Fatalf("%s unmatched note-off", f.ID)
				}
				delete(held, e.Note)
				if pedal {
					pending[e.Note] = true
				}
			case "pedal":
				pedal = e.Pedal >= .5
				if !pedal {
					clear(pending)
				}
			default:
				t.Fatalf("unexpected event %s", e.Kind)
			}
			if len(held)+len(pending) > 8 {
				t.Fatalf("%s exceeds model polyphony", f.ID)
			}
		}
		if len(held) > 0 || pedal {
			t.Fatalf("%s leaves keys or pedal held", f.ID)
		}
	}
}

func TestSamplePedalDefersDampingAndKeepsNaturalDecay(t *testing.T) {
	r := sample.Region{Left: make([]float32, outputRate), SampleRate: outputRate, RootKey: 60, End: outputRate}
	for i := range r.Left {
		r.Left[i] = .1
	}
	v, err := sample.New(outputRate, r)
	if err != nil {
		t.Fatal(err)
	}
	s := &sampledInstrument{prepared: map[[2]uint8]preparedNote{{60, 127}: {attack: [2]sample.Voice{*v}, attackCount: 1}}, releaseFrames: outputRate / 5}
	if err := s.SetSustain(1); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteOn(60, 127); err != nil {
		t.Fatal(err)
	}
	s.NoteOff(60)
	for i := 0; i < outputRate/4; i++ {
		l, _ := s.NextStereo()
		if l != .1 {
			t.Fatal("pedal suppressed natural sustain")
		}
	}
	if err := s.SetSustain(0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < outputRate/5; i++ {
		s.NextStereo()
	}
	l, _ := s.NextStereo()
	if l != 0 {
		t.Fatalf("pedal release failed to damp sample: %g", l)
	}
	allocations := testing.AllocsPerRun(100, func() {
		s.Reset()
		_ = s.NoteOn(60, 127)
		_ = s.SetSustain(1)
		s.NoteOff(60)
		for i := 0; i < 128; i++ {
			s.NextStereo()
		}
		_ = s.SetSustain(0)
		for i := 0; i < 128; i++ {
			s.NextStereo()
		}
	})
	if allocations != 0 {
		t.Fatalf("prepared sample playback allocated %g times", allocations)
	}
}

func TestPackVerifiesCompressedAndDecodedHashes(t *testing.T) {
	dir := t.TempDir()
	wav, err := encodeWAV(audio{left: []float32{.1, .2, .3}, right: []float32{.2, .3, .4}})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(wav); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	a := packAsset{ID: "fixture", Path: "sample.wav.gz", SHA256: fmt.Sprintf("%x", sha256.Sum256(compressed.Bytes())), WAVSHA256: fmt.Sprintf("%x", sha256.Sum256(wav)), SourceSHA256: fmt.Sprintf("%x", sha256.Sum256(wav)), SourceURL: "https://example.com/sample.wav", License: "CC0-1.0", LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/", Bytes: compressed.Len(), WAVBytes: len(wav), Rate: outputRate, Channels: 2, Frames: 3}
	m := packManifest{Format: "cicada.instrument-pack/1", ID: "grand", Assets: []packAsset{a}, Zones: []zone{{Asset: a.ID, Root: 60, KeyLow: 21, KeyHigh: 108, VelocityLow: 1, VelocityHigh: 127, Layer: 88, Gain: 1, Count: 1}}}
	m.Config.Gain = 1
	manifest, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, a.Path), compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := loadPack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.region(m.Zones[0]); err != nil {
		t.Fatal(err)
	}
	p, err = loadPack(dir)
	if err != nil {
		t.Fatal(err)
	}
	asset := p.assets[a.ID]
	asset.WAVSHA256 = fmt.Sprintf("%064x", 0)
	p.assets[a.ID] = asset
	if _, err := p.region(m.Zones[0]); err == nil {
		t.Fatal("accepted wrong decoded WAV hash")
	}
	p, err = loadPack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, a.Path), append(compressed.Bytes(), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.region(m.Zones[0]); err == nil {
		t.Fatal("accepted corrupt compressed grand asset")
	}
}
