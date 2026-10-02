package liveplay

import (
	"fmt"
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

func drainNotes(t *testing.T, p *Player) []Event {
	t.Helper()
	if _, err := p.Read(make([]byte, blockFrames*8)); err != nil {
		t.Fatal(err)
	}
	var events []Event
	for len(p.events) > 0 {
		event := <-p.events
		if event.Kind == "note-on" || event.Kind == "note-off" {
			events = append(events, event)
		}
	}
	return events
}

func TestLiveNoteIDsIgnoreLateReleaseAndRestorePreviousOwner(t *testing.T) {
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, n := range []struct {
		id    string
		pitch int
	}{{"midi:a", 60}, {"keyboard:b", 64}} {
		if err := p.NoteID("bass", n.pitch, 100, true, n.id); err != nil {
			t.Fatal(err)
		}
	}
	if got := drainNotes(t, p); len(got) != 2 {
		t.Fatalf("two owned presses: %+v", got)
	}
	// An incorrect pitch/track or an unknown ID must never close the current gate.
	for _, n := range []struct {
		id    string
		pitch int
	}{{"midi:a", 61}, {"unknown", 64}} {
		if err := p.NoteID("bass", n.pitch, 0, false, n.id); err != nil {
			t.Fatal(err)
		}
	}
	if got := drainNotes(t, p); len(got) != 0 {
		t.Fatalf("unmatched release reached the voice: %+v", got)
	}
	if err := p.NoteID("bass", 64, 0, false, "keyboard:b"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 1 || got[0].Kind != "note-on" {
		t.Fatalf("previous owner was not restored: %+v", got)
	}
	if err := p.NoteID("bass", 60, 0, false, "midi:a"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 1 || got[0].Kind != "note-off" {
		t.Fatalf("final owner was not released: %+v", got)
	}
	if err := p.NoteID("bass", 60, 0, false, "midi:a"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 0 {
		t.Fatalf("duplicate release: %+v", got)
	}
}

func TestLiveNoteIDsShareOwnershipAcrossClientsAndDrumAliases(t *testing.T) {
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, id := range []string{"client-a", "client-b"} {
		if err := p.NoteID("bass", 60, 100, true, id); err != nil {
			t.Fatal(err)
		}
	}
	drainNotes(t, p)
	if err := p.NoteID("bass", 60, 0, false, "client-a"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 0 {
		t.Fatalf("earlier client's release silenced shared pitch: %+v", got)
	}
	if err := p.NoteID("bass", 60, 0, false, "client-b"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 1 || got[0].Kind != "note-off" {
		t.Fatalf("last client's release: %+v", got)
	}
	for _, n := range []struct {
		id    string
		pitch int
	}{{"low-tom-a", 41}, {"low-tom-b", 43}, {"kick", 36}} {
		if err := p.NoteID("drums", n.pitch, 100, true, n.id); err != nil {
			t.Fatal(err)
		}
	}
	drainNotes(t, p)
	if err := p.NoteID("drums", 41, 0, false, "low-tom-a"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 0 {
		t.Fatalf("GM alias released the newer lane owner: %+v", got)
	}
	if err := p.NoteID("drums", 36, 0, false, "kick"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 1 || got[0].Kind != "note-off" {
		t.Fatalf("independent drum lane release: %+v", got)
	}
}

func TestOwnedLiveNoteAudioReaderDoesNotAllocate(t *testing.T) {
	p, err := New(noteScore(t), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	batch := &noteBatch{count: 4}
	batch.inputs[0] = noteInput{ID: "one", Track: "bass", Note: 60, Velocity: 100, On: true}
	batch.inputs[1] = noteInput{ID: "two", Track: "bass", Note: 64, Velocity: 100, On: true}
	batch.inputs[2] = noteInput{ID: "two", Track: "bass", Note: 64, On: false}
	batch.inputs[3] = noteInput{ID: "one", Track: "bass", Note: 60, On: false}
	pcm := make([]byte, blockFrames*8)
	allocs := testing.AllocsPerRun(100, func() {
		p.notes.Store(batch)
		if _, err := p.Read(pcm); err != nil {
			panic(err)
		}
		for len(p.events) > 0 {
			<-p.events
		}
	})
	if allocs != 0 {
		t.Fatalf("owned live notes allocated on audio reader: %g", allocs)
	}
	t.Logf("owned native input callback allocations/run: %g", allocs)
}

func TestLiveNoteQueueReservesReleaseCapacityWhenPressAdmissionIsFull(t *testing.T) {
	p, err := New(noteScore(t), 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := 0; i < 128; i++ {
		if err := p.NoteID("bass", i, 100, true, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.NoteID("bass", 60, 100, true, "overflow"); err == nil {
		t.Fatal("press queue admitted overflow")
	}
	for i := 0; i < 128; i++ {
		if err := p.NoteID("bass", i, 0, false, fmt.Sprint(i)); err != nil {
			t.Fatalf("release blocked by full press queue: %v", err)
		}
	}
	drainNotes(t, p)
	for _, order := range p.heldOrder {
		if order != 0 {
			t.Fatal("panic left a held owner")
		}
	}
}
