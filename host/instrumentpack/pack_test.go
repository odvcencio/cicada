package instrumentpack

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"m31labs.dev/cicada/kernel/voice/sample"
	"os"
	"path/filepath"
	"testing"
)

func fixturePack(t *testing.T) (Manifest, []byte, []byte) {
	t.Helper()
	wav := make([]byte, 44+2048)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 48000)
	binary.LittleEndian.PutUint32(wav[28:], 96000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 2048)
	for i := 44; i < len(wav); i += 2 {
		binary.LittleEndian.PutUint16(wav[i:], 8192)
	}
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	g.Write(wav)
	g.Close()
	packed := b.Bytes()
	a := Asset{ID: "take", Path: "samples/take.wav.gz", SHA256: digest(packed), Bytes: int64(len(packed)), WAVSHA256: digest(wav), WAVBytes: int64(len(wav)), Frames: 1024, Rate: 48000, Channels: 1, SourceURL: "https://example.org/take.wav", SourceSHA256: digest(wav), License: "CC0-1.0", LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/"}
	m := Manifest{Format: Format, ID: "fixture", Config: sample.DefaultInstrumentConfig(), Assets: []Asset{a}, Zones: []Zone{{Asset: "take", Root: 60, KeyLow: 60, KeyHigh: 60, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Count: 1, Gain: 1}}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return m, data, packed
}
func TestLoadPinsAndDecodesBeforePublication(t *testing.T) {
	m, data, packed := fixturePack(t)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "samples"), 0700)
	os.WriteFile(filepath.Join(dir, "samples/take.wav.gz"), packed, 0600)
	os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600)
	p, err := Load(dir, "manifest.json", digest(data))
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.New(48000)
	if err != nil {
		t.Fatal(err)
	}
	v.NoteOn(60, 127)
	var x float32
	for i := 0; i < 200; i++ {
		x, _ = v.NextStereo()
	}
	if x != .25 {
		t.Fatal(x)
	}
	if _, err = Load(dir, "manifest.json", digest(packed)); err == nil {
		t.Fatal("bad manifest pin admitted")
	}
	packed[0] ^= 1
	if _, err = DecodeAsset(m.Assets[0], packed); err == nil {
		t.Fatal("corrupt sample admitted")
	}
}
func TestManifestRejectsLicenceTraversalAndResourceBombs(t *testing.T) {
	m, _, _ := fixturePack(t)
	for _, mutate := range []func(*Manifest){func(m *Manifest) { m.Assets[0].Path = "../escape.wav.gz" }, func(m *Manifest) { m.Assets[0].License = "personal-use" }, func(m *Manifest) { m.Assets[0].License = "CC-BY-4.0" }, func(m *Manifest) { m.Assets[0].Frames = 1 << 30 }, func(m *Manifest) { m.Zones[0].Root = 256 }, func(m *Manifest) { m.Assets[0].SourceURL = "http://example.org/a" }} {
		original, _ := json.Marshal(m)
		var bad Manifest
		json.Unmarshal(original, &bad)
		mutate(&bad)
		b, _ := json.Marshal(bad)
		if _, err := DecodeManifest(b); err == nil {
			t.Fatal("invalid pack admitted")
		}
	}
	b, _ := json.Marshal(m)
	b = append(b, []byte(" {}")...)
	if _, err := DecodeManifest(b); err == nil {
		t.Fatal("trailing JSON")
	}
	dir := t.TempDir()
	other := t.TempDir()
	_, data, packed := fixturePack(t)
	os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600)
	os.Mkdir(filepath.Join(other, "samples"), 0700)
	os.WriteFile(filepath.Join(other, "samples/take.wav.gz"), packed, 0600)
	os.Symlink(filepath.Join(other, "samples"), filepath.Join(dir, "samples"))
	if _, err := Load(dir, "manifest.json", ""); err == nil {
		t.Fatal("escaping symlink")
	}
}
