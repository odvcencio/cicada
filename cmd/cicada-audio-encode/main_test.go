package main

import (
	"bytes"
	"encoding/json"
	"m31labs.dev/cicada/host/audioencoding"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEncodeExactFallbackAndPreserveExistingFiles(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.wav")
	pcm := [][]float32{{0, 1.0 / (1 << 20), -1.0 / (1 << 25)}}
	wav, _ := pcm16WAV(pcm, 48000, 1.0/(1<<20))
	if e := os.WriteFile(input, wav, 0600); e != nil {
		t.Fatal(e)
	}
	stem := filepath.Join(dir, "asset")
	args := []string{"-in", input, "-out", stem, "-ffmpeg", filepath.Join(dir, "absent")}
	if e := run(args, new(bytes.Buffer)); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(stem + ".wav.gz.json")
	if e != nil {
		t.Fatal(e)
	}
	var d descriptor
	if e = json.Unmarshal(b, &d); e != nil {
		t.Fatal(e)
	}
	encoded, _ := os.ReadFile(stem + ".wav.gz")
	if _, e = audioencoding.Decode(d.Asset, encoded); e != nil {
		t.Fatal(e)
	}
	if e = run(args, new(bytes.Buffer)); e == nil {
		t.Fatal("existing files overwritten")
	}
	after, _ := os.ReadFile(stem + ".wav.gz")
	if !bytes.Equal(after, encoded) {
		t.Fatal("existing asset changed")
	}
}
func TestHQ16EncodeAndChecksum(t *testing.T) {
	encoder, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg is needed for offline FLAC export")
	}
	dir := t.TempDir()
	pcm := [][]float32{{0, .25, -.25, .124993, -.124993}}
	wav, _ := pcm16WAV(pcm, 48000, 1)
	input := filepath.Join(dir, "input.wav")
	os.WriteFile(input, wav, 0600)
	stem := filepath.Join(dir, "asset")
	if e = run([]string{"-in", input, "-out", stem, "-tier", "hq16", "-ffmpeg", encoder}, new(bytes.Buffer)); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(stem + ".flac.json")
	var d descriptor
	json.Unmarshal(b, &d)
	encoded, _ := os.ReadFile(stem + ".flac")
	p, e := audioencoding.Decode(d.Asset, encoded)
	if e != nil {
		t.Fatal(e)
	}
	if d.Tier != "hq16" || d.Asset.Scale != .25 || len(p[0]) != 5 {
		t.Fatal(d)
	}
	if e = run([]string{"-in", input, "-out", stem, "-tier", "invalid"}, new(bytes.Buffer)); e == nil {
		t.Fatal("invalid tier accepted")
	}
}
