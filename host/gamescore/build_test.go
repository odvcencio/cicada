package gamescore

import (
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/gosxhost"
)

func loadExample(t *testing.T, name string) *project.Project {
	t.Helper()
	score, ds, err := project.LoadScore(filepath.Join("..", "..", "examples", name), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	return p
}

func TestBuildExampleScores(t *testing.T) {
	for _, name := range []string{"game-director.cicada", "game-music.cicada"} {
		for _, rate := range []int{44100, 48000, 96000} {
			p := loadExample(t, name)
			pack, err := Build(p, filepath.Join("..", "..", "examples"), rate)
			if err != nil {
				t.Fatalf("%s at %d Hz: %v", name, rate, err)
			}
			data, err := pack.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			got, err := gosxhost.ParsePack(data)
			if err != nil {
				t.Fatalf("%s at %d Hz: %v", name, rate, err)
			}
			if got.SampleRate != rate || got.Surface.SampleRate != rate {
				t.Fatalf("%s: rate %d/%d, want %d", name, got.SampleRate, got.Surface.SampleRate, rate)
			}
			if got.Kernel != gosxhost.KernelCore {
				t.Fatalf("%s needs kernel flavor %d, want the core kernel", name, got.Kernel)
			}
			if got.TempoMilli == 0 || len(got.Surface.States) != 2 {
				t.Fatalf("%s: tempo %d states %d", name, got.TempoMilli, len(got.Surface.States))
			}
		}
	}
}

func TestBuildRefusesScoreWithoutLiveBlock(t *testing.T) {
	score, ds, err := project.LoadScore(filepath.Join("..", "..", "examples", "first-acid.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ds
	p, _ := project.FromScore(score)
	if _, err := Build(p, filepath.Join("..", "..", "examples"), 48000); err == nil {
		t.Fatal("Build accepted a score without a live block")
	}
}

func TestImageCapabilitiesAddsVersionBits(t *testing.T) {
	image := make([]byte, 32)
	image[4] = 15 // version 15 needs unified-image support (bit 16)
	if got := ImageCapabilities(image); got != 1<<16 {
		t.Fatalf("v15 capabilities = %#x", got)
	}
	image[4] = 18
	if got := ImageCapabilities(image); got != 1<<16|1<<18|1<<19|1<<21 {
		t.Fatalf("v18 capabilities = %#x", got)
	}
	image[4], image[30] = 13, 0x01
	if got := ImageCapabilities(image); got != 1 {
		t.Fatalf("v13 capabilities = %#x", got)
	}
}
