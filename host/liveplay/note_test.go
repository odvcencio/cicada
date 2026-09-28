package liveplay

import (
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

func noteScore(t *testing.T) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 2, MaxVoices: 16, BPMMilli: 120_000}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Track[1].Kind = engine.VoiceDrums
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 120_000,
		Tracks: []TrackSlots{{ID: "bass", Kind: "acid"}, {ID: "drums", Kind: "drums"}},
	}
}

func TestLiveNotesValidateAndReachTheEngine(t *testing.T) {
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		track          string
		note, velocity int
		on             bool
	}{
		{track: "bass", note: 60, velocity: 100, on: true},
		{track: "drums", note: 36, velocity: 90, on: true},
	} {
		if err := p.Note(input.track, input.note, input.velocity, input.on); err != nil {
			t.Fatalf("queue %s note-on: %v", input.track, err)
		}
	}
	if _, err := p.Read(make([]byte, blockFrames*8)); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for len(p.events) > 0 {
		event := <-p.events
		if event.Kind == "note-on" {
			seen[event.Track] = true
		}
	}
	if !seen["bass"] || !seen["drums"] {
		t.Fatalf("live note commands were not applied for both tracks: %+v", seen)
	}
	for _, input := range []struct {
		track string
		note  int
	}{
		{track: "bass", note: 60},
		{track: "drums", note: 36},
	} {
		if err := p.Note(input.track, input.note, 0, false); err != nil {
			t.Fatalf("queue %s note-off: %v", input.track, err)
		}
	}
	if _, err := p.Read(make([]byte, blockFrames*8)); err != nil {
		t.Fatal(err)
	}
	seen = map[string]bool{}
	for len(p.events) > 0 {
		event := <-p.events
		if event.Kind == "note-off" {
			seen[event.Track] = true
		}
	}
	if !seen["bass"] || !seen["drums"] {
		t.Fatalf("live note-off commands were not applied for both tracks: %+v", seen)
	}
	if p.fault != nil {
		t.Fatalf("valid live notes faulted the engine: %v", p.fault)
	}
}

func TestLiveNotesRejectInvalidTracksAndRangesWithoutFaulting(t *testing.T) {
	for _, mutate := range []func(*Score){
		func(score *Score) { score.Tracks[0].Kind = "instrument" },
		func(score *Score) { score.Tracks[0].ID = "renamed" },
	} {
		score := noteScore(t)
		mutate(&score)
		p, err := New(score, 48_000)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Note("bass", 60, 100, true); err == nil {
			t.Fatal("accepted a note for a non-acid or unknown track")
		}
		if p.fault != nil {
			t.Fatalf("invalid live note faulted the engine: %v", p.fault)
		}
	}
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		track          string
		note, velocity int
	}{
		{track: "bass", note: -1, velocity: 100},
		{track: "bass", note: 128, velocity: 100},
		{track: "bass", note: 60, velocity: -1},
		{track: "bass", note: 60, velocity: 128},
		{track: "drums", note: 60, velocity: 100},
	} {
		if err := p.Note(input.track, input.note, input.velocity, true); err == nil {
			t.Errorf("accepted invalid note: %+v", input)
		}
	}
	if _, err := p.Read(make([]byte, blockFrames*8)); err != nil {
		t.Fatal(err)
	}
	if p.fault != nil {
		t.Fatalf("rejected live notes faulted the engine: %v", p.fault)
	}
}

func TestGeneralMIDIDrumMap(t *testing.T) {
	want := map[int]uint16{
		36: 0, 37: 5, 38: 1, 39: 4, 41: 6, 43: 6, 42: 2,
		45: 7, 47: 7, 46: 3, 48: 8, 50: 8, 49: 10, 57: 10, 56: 9,
	}
	for note, lane := range want {
		got, ok := GMDrumLane(note)
		if !ok || got != lane {
			t.Errorf("GM note %d maps to lane %d, %v; want %d", note, got, ok, lane)
		}
	}
	if _, ok := GMDrumLane(60); ok {
		t.Fatal("mapped unsupported General MIDI note 60")
	}
}
