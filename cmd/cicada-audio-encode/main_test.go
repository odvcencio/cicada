package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/audioencoding"
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

func TestFLACPreservesShortSamplesAndFinalFrames(t *testing.T) {
	encoder, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is needed for offline FLAC export")
	}
	for _, tier := range []string{"lossless", "hq16"} {
		for _, rate := range []int{44100, 48000} {
			for _, channels := range []int{1, 2} {
				for _, frames := range []int{1, 5, 15, 16, 17, 4609} {
					t.Run(fmt.Sprintf("%s/%dHz/%dch/%dframes", tier, rate, channels, frames), func(t *testing.T) {
						dir := t.TempDir()
						pcm := make([][]float32, channels)
						want := make([][]float32, channels)
						values := [...]float32{.25, -.25, .125, -.125, 0}
						for ch := range pcm {
							pcm[ch], want[ch] = make([]float32, frames), make([]float32, frames)
							for i := range pcm[ch] {
								x := values[(i+ch)%len(values)]
								pcm[ch][i], want[ch][i] = x, x
								if tier == "hq16" && x == .25 {
									want[ch][i] = 32767.0 / 131072
								}
							}
						}
						wav, _ := pcm16WAV(pcm, rate, 1)
						input, stem := filepath.Join(dir, "source.wav"), filepath.Join(dir, "asset")
						if err := os.WriteFile(input, wav, 0600); err != nil {
							t.Fatal(err)
						}
						if err := run([]string{"-in", input, "-out", stem, "-tier", tier, "-ffmpeg", encoder}, new(bytes.Buffer)); err != nil {
							t.Fatal(err)
						}
						metadata, err := os.ReadFile(stem + ".flac.json")
						if err != nil {
							t.Fatal(err)
						}
						var d descriptor
						if err := json.Unmarshal(metadata, &d); err != nil {
							t.Fatal(err)
						}
						encoded, err := os.ReadFile(stem + ".flac")
						if err != nil {
							t.Fatal(err)
						}
						decoded, err := audioencoding.Decode(d.Asset, encoded)
						if err != nil {
							t.Fatal(err)
						}
						if d.Asset.Frames != frames || d.Asset.Rate != rate || d.Asset.Channels != channels || d.Asset.PCMHash != audioencoding.PCMHash(want) || audioencoding.PCMHash(decoded) != audioencoding.PCMHash(want) {
							t.Fatal("FLAC changed sample dimensions or PCM", d.Asset)
						}
						if d.SourceSHA256 != digest(wav) || d.Tier != tier || d.Asset.Encoding != "flac" {
							t.Fatal("FLAC descriptor changed source or tier", d)
						}
						wantScale := float32(1)
						if tier == "hq16" {
							wantScale = .25
						}
						if d.Asset.Scale != wantScale {
							t.Fatal("FLAC changed amplitude scale", d.Asset.Scale)
						}
					})
				}
			}
		}
	}
}
