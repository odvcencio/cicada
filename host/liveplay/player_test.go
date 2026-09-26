package liveplay

import (
	"io"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

func testScore(t *testing.T, name string, bpmMilli int64) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: bpmMilli}
	cfg.Track[0].Kind = engine.VoiceAcid
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: bpmMilli, Name: name}
}

func TestValidatedEditsReplaceAtExactBarAndUseNewTempo(t *testing.T) {
	p, err := New(testScore(t, "first", 120_000), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, (96_000-1)*8); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(testScore(t, "superseded", 120_000)); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(testScore(t, "latest", 60_000)); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("edit landed before bar boundary: %+v", event)
	default:
	}
	// A one-byte read must start the next frame and land the newest edit.
	if _, err := io.ReadFull(p, frame[:1]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		if event.Bar != 2 || event.Name != "latest" {
			t.Fatalf("wrong landing: %+v", event)
		}
	default:
		t.Fatal("validated edit did not land at bar 2")
	}
	if _, err := io.ReadFull(p, frame[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, (192_000-2)*8); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(testScore(t, "bar three", 60_000)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("new-tempo edit landed early: %+v", event)
	default:
	}
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		if event.Bar != 3 || event.Name != "bar three" {
			t.Fatalf("wrong bar-three landing: %+v", event)
		}
	default:
		t.Fatal("new-tempo bar was not reached")
	}
}

func TestLivePlayerRejectsMismatchedSampleRate(t *testing.T) {
	wrong := testScore(t, "wrong", 120_000)
	wrong.SampleRate = 44_100
	if _, err := New(wrong, 48_000); err == nil {
		t.Fatal("accepted a score built for a different sample rate")
	}
	p, err := New(testScore(t, "initial", 120_000), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(wrong); err == nil {
		t.Fatal("queued a score built for a different sample rate")
	}
}

func TestPositionTracksRenderedBarAndStep(t *testing.T) {
	p, err := New(testScore(t, "position", 120_000), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Position(); got != (Position{Bar: 1, Step: 1}) {
		t.Fatalf("initial position: %+v", got)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	if got := p.Position(); got != (Position{Bar: 2, Step: 1}) {
		t.Fatalf("second bar position: %+v", got)
	}
}
