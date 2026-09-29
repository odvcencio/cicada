package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestFirstAcidCompilesIntoLiveEngine(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "testdata", "edition1", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source parse: %+v", diagnostic)
		}
	}
	project, diagnostics := FromScore(score)
	if project == nil {
		t.Fatalf("project compile: %+v", diagnostics)
	}
	cfg, err := CompileEngine(project, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tracks != 3 || len(cfg.Scenes) != 2 || len(cfg.Song) != 2 || cfg.Patterns[1].Drums == nil {
		t.Fatalf("incomplete engine configuration: tracks=%d scenes=%d song=%d drums=%v", cfg.Tracks, len(cfg.Scenes), len(cfg.Song), cfg.Patterns[1].Drums != nil)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play command rejected")
	}
	var left, right [128]float32
	var heard [3]bool
	energy := float64(0)
	for range 128 {
		e.Render(left[:], right[:])
		for _, sample := range left {
			energy += float64(sample * sample)
		}
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("live first-acid fault %d", message.A)
			}
			if message.Kind == cmd.NoteOn && message.Tick == 0 && message.Track < 3 {
				heard[message.Track] = true
			}
		}
	}
	if energy == 0 || !heard[0] || !heard[1] || !heard[2] {
		t.Fatalf("first-acid did not play all tracks: energy=%g heard=%v", energy, heard)
	}
	for i := range project.Patterns {
		if project.Patterns[i].Kind == "drums" {
			project.Patterns[i].Transpose = 1
			break
		}
	}
	if err := ValidateProject(project); err == nil {
		t.Fatal("semantic project accepted unsupported drum transpose")
	}
	if _, err := CompileEngine(project, 48_000, 128); err == nil {
		t.Fatal("unsupported drum transpose was silently accepted")
	}
}

func TestSceneSyncedDelayDivisionValidatesAndCompiles(t *testing.T) {
	source := []byte("fx delay {}\ntrack bass acid {}\npattern riff acid { 1 }\nscene main { bass = riff delay.time = 1/8 }\nsong { main }\n")
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source parse: %+v", diagnostic)
		}
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("source validation: %+v", diagnostics)
	}
	if err := ValidateProject(p); err != nil {
		t.Fatalf("semantic validation rejected a registered synced delay division: %v", err)
	}
	cfg, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatalf("validated synced delay division did not compile: %v", err)
	}
	if len(cfg.Scenes) != 1 || len(cfg.Scenes[0].Settings) != 1 || cfg.Scenes[0].Settings[0].Division.String() != "1/8" {
		t.Fatalf("scene lost its synced delay division: %+v", cfg.Scenes)
	}
}

func TestSceneSyncedDelayDivisionMustFitAtScoreTempo(t *testing.T) {
	source := []byte("tempo 20\nfx delay {}\ntrack bass acid {}\npattern riff acid { 1 }\nscene main { bass = riff delay.time = 1/2 }\nsong { main }\n")
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source parse: %+v", diagnostic)
		}
	}
	p, diagnostics := FromScore(score)
	if p != nil {
		t.Fatal("scene accepted a synced delay division longer than the four-second buffer")
	}
	if len(diagnostics) == 0 || diagnostics[len(diagnostics)-1].Code != "CICADA-PARAM" || !strings.Contains(diagnostics[len(diagnostics)-1].Message, "delay.time") || !strings.Contains(diagnostics[len(diagnostics)-1].Message, "1/2") {
		t.Fatalf("oversized synced scene delay lacks its path, value, and diagnostic: %+v", diagnostics)
	}
}
