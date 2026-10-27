package project

import (
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

// One track of each kind whose reservation differs. The organ has no chord
// pattern, so its poly instrument takes eight voices; the strings place one and
// take four.
const trackVoicesSource = `cicada 2
tempo 120
instrument bell { octave = 4 voice mono { out = sine(pitch) * env(gate, 200ms) * 0.2 } }
instrument harp { octave = 4 voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
instrument pad { octave = 4 voice poly { out = sine(pitch) * env(gate, 300ms) * 0.1 } }
kit trio {
  bd = model.kick
  sd = model.snare
  ch = model.hat_closed
}
track lead acid {}
track chime bell {}
track strings harp {}
track organ pad {}
track vibes model_vibraphone {}
track grand piano {}
track drums drums {}
track perc trio {}
pattern riff acid { 1 . 5 . }
pattern note notes steps=4 { c4 . . . }
pattern triad notes steps=4 { [c4 e4 g4] . . . }
pattern beat drums { bd: x... x... }
scene main { lead = riff chime = note strings = triad organ = note vibes = note grand = note drums = beat perc = beat }
song { main }
`

func TestTrackVoicesCountsEachReservation(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(trackVoicesSource))
	if score == nil || hasErrors(diagnostics) {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasErrors(diagnostics) {
		t.Fatalf("compile: %+v", diagnostics)
	}
	cfg, err := CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"lead": 1, "chime": 1, "strings": 4, "organ": 8, "vibes": 4, "grand": 8, "drums": 6, "perc": 3}
	total := 0
	for i, track := range p.Tracks {
		got := TrackVoices(&cfg.Track[i])
		if got != want[track.ID] {
			t.Errorf("track %s reserves %d voices, want %d", track.ID, got, want[track.ID])
		}
		total += got
	}
	if total != 35 {
		t.Fatalf("reservations total %d, want 35", total)
	}
	// Over 32 the image capacity is exactly the sum, so the report and the
	// portable image's limit count the same voices.
	if cfg.MaxVoices != total {
		t.Fatalf("image capacity %d differs from the %d voices the tracks reserve", cfg.MaxVoices, total)
	}
	// The engine allocates the same: it loads at the total and refuses one less.
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine refused %d voices: %v", total, err)
	}
	e.Close()
	cfg.MaxVoices--
	if _, err := engine.New(cfg); err == nil {
		t.Fatal("engine allocated fewer voices than the tracks reserve")
	}
}
