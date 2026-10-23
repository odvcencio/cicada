package project

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

const performedScore = `tempo 120
track keys piano {}
pattern phrase performance length=7680ticks {
  note 60 at=17ticks length=703ticks velocity=82;
  note 64 at=19ticks length=1201ticks velocity=105;
  note 67 at=501ticks length=101ticks velocity=47;
}
scene main { keys=phrase }
song { main*4 }
`

func TestPerformanceSourceAndJSONKeepIndependentGates(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(performedScore))
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("compile: %+v", diagnostics)
	}
	performance := p.Patterns[0].Performance
	if performance == nil || performance.LengthTicks != 7680 || len(performance.Notes) != 3 || performance.Notes[1].Tick != 19 || performance.Notes[1].EndTick != 1220 || performance.Notes[1].Velocity != 105 {
		t.Fatalf("source lost performed timing: %+v", performance)
	}
	document, err := notation.ParseDocument([]byte(performedScore))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	formattedScore, fd := notation.Parse(formatted)
	formattedProject, fd := FromScore(formattedScore)
	if formattedProject == nil || len(fd) != 0 || len(formattedProject.Patterns[0].Performance.Notes) != 3 {
		t.Fatalf("formatter changed performed syntax: %v\n%s", fd, formatted)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("performance length=7680ticks")) || !bytes.Contains(source, []byte("note 64 at=19ticks length=1201ticks velocity=105")) {
		t.Fatalf("source flattened performed gates\n%s", source)
	}
	parsed, diagnostics := notation.Parse(source)
	if parsed == nil || len(diagnostics) != 0 {
		t.Fatalf("canonical source: %+v\n%s", diagnostics, source)
	}
	again, diagnostics := FromScore(parsed)
	if again == nil || len(diagnostics) != 0 {
		t.Fatalf("canonical compile: %+v", diagnostics)
	}
	got, _ := json.Marshal(again)
	if !bytes.Equal(data, got) {
		t.Fatalf("performance round trip changed\n%s\n%s", data, got)
	}
	cfg, err := CompileEngine(again, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Patterns[0].Slots[0].DurationTicks() != 7680 {
		t.Fatal("runtime reduced a performance to one grid step")
	}
	player, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
}

func TestPerformanceRejectsGridSettingsAndInvalidGates(t *testing.T) {
	for _, change := range [][2]string{
		{"length=7680ticks", "length=0ticks"},
		{"at=17ticks", "at=8000ticks"},
		{"velocity=82", "velocity=128"},
		{"note 60", "note 256"},
		{"note 64", "note 60"},
		{"length=703ticks", "length=0ticks"},
		{"length=7680ticks", "length=7680ticks swing=56%"},
	} {
		source := strings.Replace(performedScore, change[0], change[1], 1)
		score, diagnostics := notation.Parse([]byte(source))
		if score == nil || hasErrors(diagnostics) {
			continue
		}
		if p, diagnostics := FromScore(score); p != nil && !hasErrors(diagnostics) {
			t.Fatalf("accepted invalid performance: %v", change)
		}
	}
}
