package edit_test

import (
	"bytes"
	"encoding/json"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/phrase"
	"m31labs.dev/cicada/project"
	"os"
	"strings"
	"testing"
)

func TestMutationKeepsThePreviewArrangement(t *testing.T) {
	p := phrase.DefaultParams()
	p.Seed = 4242
	p.Structure = phrase.ABAC
	generated, err := phrase.Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	variation, err := phrase.Mutate(p, generated.Bars[0], []phrase.Op{{Kind: phrase.ToggleAccent}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := edits.ReplacePreviewBar(generated.Notation, variation.Notation)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := notation.Parse([]byte(generated.Notation))
	after, _ := notation.Parse([]byte(updated))
	if len(after.Song) != len(before.Song) || len(after.Patterns) != len(before.Patterns) || len(after.Scenes) != len(before.Scenes) {
		t.Fatal("mutation dropped the arrangement")
	}
	if before.KeyRoot != after.KeyRoot || before.Scale != after.Scale || before.TempoMilli != after.TempoMilli || before.Title != after.Title {
		t.Fatal("mutation changed the preview header")
	}
	for i := range before.Song {
		if after.Song[i].Scene != before.Song[i].Scene {
			t.Fatal("mutation changed scene order")
		}
	}
	bars, err := project.CompilePattern(after, after.Patterns[0], after.Tracks[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) == 0 || bars[0].Pattern.Steps[0] != generated.Bars[0].Steps[0] {
		t.Fatal("preview lost a locked step")
	}
}

func TestReplacePreviewBarLegacyParity(t *testing.T) {
	data, err := os.ReadFile("testdata/preview-bar-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var captures []struct{ Original, Variation, Updated string }
	if err := json.Unmarshal(data, &captures); err != nil {
		t.Fatal(err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected two legacy generator captures, got %d", len(captures))
	}
	for _, capture := range captures {
		for _, newline := range []string{"\n", "\r\n"} {
			original := strings.ReplaceAll(capture.Original, "\n", newline)
			variation := strings.ReplaceAll(capture.Variation, "\n", newline)
			got, err := edits.ReplacePreviewBar(original, variation)
			want := strings.ReplaceAll(capture.Updated, "\n", newline)
			if err != nil || got != want {
				t.Fatalf("preview parity: got %q, want %q, %v", got, want, err)
			}
			before, _ := notation.Parse([]byte(original))
			changed := strings.Replace(variation, "pattern "+before.Patterns[0].Name, "pattern changed", 1)
			if _, err := edits.ReplacePreviewBar(original, changed); err == nil || err.Error() != "phrase preview pattern changed" {
				t.Fatalf("mismatch error %v", err)
			}
			if _, err := edits.ReplacePreviewBar(original, "seed 4"); err == nil || err.Error() != "phrase preview has no acid bar" {
				t.Fatalf("missing bar error %v", err)
			}
			// Declaration spans are byte exact, including the surrounding arrangement.
			if !bytes.Contains([]byte(got), []byte("song {")) {
				t.Fatal("preview lost the song")
			}
		}
	}
}
