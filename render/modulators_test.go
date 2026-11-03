package render

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"m31labs.dev/cicada/notation"
)

const modulatedSong = `tempo 120
track lead acid { level = -6dB }
pattern riff { 1 . 3 . 5 . 3 . }
scene main { lead = riff }
song { main*4 }
`

func renderBytes(t *testing.T, source string) []byte {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatal(ds)
	}
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(ds)
		}
	}
	var out bytes.Buffer
	if _, err := WAV(score, Options{}, &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Modulators play: the same score with a pan and a level route renders to
// different bytes than without them, twice the same, and takes the engine path.
func TestModulatorsRenderThroughTheEngine(t *testing.T) {
	plain := renderBytes(t, modulatedSong)
	source := modulatedSong + "lfo drift { rate = 1bar }\nlfo throb { wave = square rate = 1/4 }\nmod drift -> lead.pan depth = 0.8\nmod throb -> lead.level depth = 3dB\n"
	score, _ := notation.Parse([]byte(source))
	if !UsesScheduledEngine(score, Options{}) {
		t.Fatal("modulators did not take the engine path")
	}
	first, second := renderBytes(t, source), renderBytes(t, source)
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of the same modulated score differ")
	}
	if sha256.Sum256(first) == sha256.Sum256(plain) {
		t.Fatal("the routes changed nothing")
	}
	if len(first) != len(plain) {
		t.Fatalf("length changed: %d vs %d", len(first), len(plain))
	}
}
