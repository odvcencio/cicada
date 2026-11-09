package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/sdk/gosxhost"
)

func TestGameScoreWritesAPackTheGameCanLoad(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	out := filepath.Join(t.TempDir(), "music.cscore")
	var stdout, stderr bytes.Buffer
	if err := gameScoreCommand([]string{score, "-o", out, "--rate", "44100"}, &stdout, &stderr); err != nil {
		t.Fatalf("game-score: %v\n%s", err, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := gosxhost.ParsePack(data)
	if err != nil {
		t.Fatal(err)
	}
	if pack.SampleRate != 44100 || pack.Surface.SampleRate != 44100 {
		t.Fatalf("rate = %d / %d, want 44100", pack.SampleRate, pack.Surface.SampleRate)
	}
	if pack.TempoMilli != 120000 {
		t.Fatalf("tempo = %d milli-BPM, want 120000", pack.TempoMilli)
	}
	if len(pack.Surface.States) != 2 || pack.Surface.States[0].Name != "explore" || pack.Surface.Macros[0].Name != "intensity" {
		t.Fatalf("manifest = %+v", pack.Surface)
	}
	if pack.Kernel != gosxhost.KernelCore || !strings.Contains(stdout.String(), "cicada-kernel.wasm") {
		t.Fatalf("kernel = %d, output %q", pack.Kernel, stdout.String())
	}
	// The pack drives a Music controller end to end.
	var sent int
	music, err := gosxhost.NewMusic(pack, func(b []byte) error { sent += len(b); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := music.Start("explore"); err != nil || sent == 0 {
		t.Fatalf("Start: err=%v sent=%d", err, sent)
	}
}

func TestGameScoreRefusesBadArguments(t *testing.T) {
	score := writeDirectorScore(t, directorScore)
	cases := map[string][]string{
		"no output":     {score},
		"wrong ext":     {score, "-o", filepath.Join(t.TempDir(), "x.json")},
		"bad rate":      {score, "-o", filepath.Join(t.TempDir(), "x.cscore"), "--rate", "22050"},
		"no score":      {"-o", filepath.Join(t.TempDir(), "x.cscore")},
		"not a .cicada": {filepath.Join(t.TempDir(), "x.txt"), "-o", filepath.Join(t.TempDir(), "x.cscore")},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		if err := gameScoreCommand(args, &stdout, &stderr); err == nil {
			t.Errorf("%s: command succeeded", name)
		}
	}
}

func TestGameScoreRequiresALiveBlock(t *testing.T) {
	source := directorScore[:strings.Index(directorScore, "live {")] + directorScore[strings.Index(directorScore, "pattern bed-loop"):]
	score := writeDirectorScore(t, source)
	var stdout, stderr bytes.Buffer
	err := gameScoreCommand([]string{score, "-o", filepath.Join(t.TempDir(), "x.cscore")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "live") {
		t.Fatalf("err = %v, want a live-block refusal", err)
	}
}
