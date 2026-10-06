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
	"os"
	"testing"
)

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
	score.Samplers[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, AssetDir: dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("corrupt pin rendered")
	}
}
