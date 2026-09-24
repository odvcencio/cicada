package project

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestFirstAcidCompilesIntoLiveEngine(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "first-acid.cicada"))
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
