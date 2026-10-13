package smf

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestSceneTempoWritesOneMIDITempoEventPerChange(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "scene-tempo.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatal(diagnostics)
	}
	file, err := FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []tempoPoint
	for _, meta := range file.Tracks[0].Meta {
		if meta.Type == 0x51 {
			got = append(got, tempoPoint{meta.Tick, uint32(meta.Data[0])<<16 | uint32(meta.Data[1])<<8 | uint32(meta.Data[2])})
		}
	}
	// 120, 100, 84, 66 BPM at bars 1, 3, 4 and 5 (zero-based 0, 2, 3, 4).
	want := []tempoPoint{{0, 500000}, {2 * seq.TicksPerBar, 600000}, {3 * seq.TicksPerBar, 714286}, {4 * seq.TicksPerBar, 909091}}
	if len(got) != len(want) {
		t.Fatalf("tempo events %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tempo event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if points := tempos(decoded); len(points) != len(want) {
		t.Fatalf("decoded tempo events %+v", points)
	}
	// A range that ends before a change leaves that change out.
	short, err := FromProject(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	if count := len(short.Tracks[0].Meta); count != 3 { // tempo, time signature, key; no change
		t.Fatalf("2-bar export has %d conductor events", count)
	}
}
