package render

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
	"math"
	"os"
	"strings"
	"testing"
)

func TestPackCrossZoneSlideReleasesPreviousOwner(t *testing.T) {
	pcm := make([]float32, 1024)
	for i := range pcm {
		pcm[i] = .25
	}
	c := sample.DefaultInstrumentConfig()
	c.Voices, c.Amp.Release = 2, 2
	p := &instrumentpack.Prepared{Manifest: instrumentpack.Manifest{Config: c}}
	for i, note := range []uint8{60, 62} {
		p.Zones = append(p.Zones, sample.Zone{Region: sample.Region{Left: pcm, SampleRate: 48000, RootKey: note, End: len(pcm), Loop: true, LoopEnd: len(pcm)}, KeyLow: note, KeyHigh: note, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Group: uint8(i), Count: 1, Gain: 1})
	}
	for _, baseline := range []bool{false, true} {
		t.Run(fmt.Sprintf("baseline=%v", baseline), func(t *testing.T) {
			var v *packVoice
			var err error
			if baseline {
				v, err = legacyBaseline(p, 48000)
			} else {
				v = &packVoice{}
				v.instrument, err = p.New(48000)
			}
			if err != nil {
				t.Fatal(err)
			}
			v.NoteOn(60, 127, false, false)
			for i := 0; i < 128; i++ {
				v.NextStereo()
			}
			if l, _ := v.NextStereo(); math.Abs(float64(l)-.25) > 1e-6 {
				t.Fatal("missing first note", l)
			}
			v.NoteOn(62, 127, false, true)
			for i := 0; i < 128; i++ {
				v.NextStereo()
			}
			if v.fault != nil {
				t.Fatal(v.fault)
			}
			if l, _ := v.NextStereo(); math.Abs(float64(l)-.25) > 1e-6 {
				t.Fatal("previous owner survived cross-zone slide", l)
			}
			v.NoteOff()
			for i := 0; i < 128; i++ {
				v.NextStereo()
			}
			if l, r := v.NextStereo(); l != 0 || r != 0 {
				t.Fatalf("slide sounded through rest/tail: %g/%g", l, r)
			}
		})
	}
}

func TestPinnedPackStereoRenderBlockAndSourceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	wav := make([]byte, 44+4096*4)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], 48000)
	binary.LittleEndian.PutUint32(wav[28:], 192000)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 4096*4)
	for i := 44; i < len(wav); i += 4 {
		value := int16((i%256 - 128) * 100)
		binary.LittleEndian.PutUint16(wav[i:], uint16(value))
		binary.LittleEndian.PutUint16(wav[i+2:], uint16(value/2))
	}
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	g.Write(wav)
	g.Close()
	hash := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
	a := instrumentpack.Asset{ID: "tone", Path: "tone.wav.gz", SHA256: hash(b.Bytes()), Bytes: int64(b.Len()), WAVSHA256: hash(wav), WAVBytes: int64(len(wav)), Frames: 4096, Rate: 48000, Channels: 2, SourceURL: "https://example.org/tone.wav", SourceSHA256: hash(wav), License: "CC0-1.0", LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/"}
	c := sample.DefaultInstrumentConfig()
	c.Voices = 2
	m := instrumentpack.Manifest{Format: instrumentpack.Format, ID: "test", Config: c, Assets: []instrumentpack.Asset{a}, Zones: []instrumentpack.Zone{{Asset: "tone", Root: 60, KeyLow: 58, KeyHigh: 62, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Count: 1, Gain: 1, Loop: true, LoopEnd: 4096, Crossfade: 64}}}
	manifest, _ := json.Marshal(m)
	os.WriteFile(dir+"/manifest.json", manifest, 0600)
	os.WriteFile(dir+"/tone.wav.gz", b.Bytes(), 0600)
	source := fmt.Sprintf(`cicada 2
sampler sound {pack="manifest.json" sha256="%s" root=c4 voices=2}
track lead sound {}
pattern melody {c4 . c4^ . d4 . c4 .}
scene main {lead=melody}
song {main}
`, hash(manifest))
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(ds)
		}
	}
	var first, second bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, TailSec: .3, Block: 64, AssetDir: dir}, &first); err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, TailSec: .3, Block: 256, AssetDir: dir}, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("block size changed pack PCM")
	}
	first.Reset()
	if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, TailSec: .3, Block: 128, AssetDir: dir, SamplerBaseline: true}, &first); err != nil {
		t.Fatal(err)
	}
	t.Run("Requested96kHz", func(t *testing.T) {
		m.Config.Cutoff = 30000
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/manifest.json", data, 0600); err != nil {
			t.Fatal(err)
		}
		rateScore, diagnostics := notation.Parse([]byte(strings.Replace(source, hash(manifest), hash(data), 1)))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatal(diagnostics)
			}
		}
		if _, err := WAV(rateScore, Options{SampleRate: 96000, Bits: 32, TailSec: .01, AssetDir: dir}, &bytes.Buffer{}); err != nil {
			t.Fatal("valid 96 kHz render rejected:", err)
		}
		if _, err := WAV(rateScore, Options{SampleRate: 48000, Bits: 32, AssetDir: dir}, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted invalid 48 kHz render")
		}
	})
	score.Samplers[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, AssetDir: dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("corrupt pin rendered")
	}
}
