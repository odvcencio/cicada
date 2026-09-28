package main

import (
	"io"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

func TestSongLandingKeepsRequestsQueuedAfterTheJump(t *testing.T) {
	_, path := studioTestHandler(t)
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.audioNull = true
	transport.stream, transport.playing = stream, true
	transport.last, err = playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.startFrom(0, "main", nil, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, stream, 8); err != nil {
		t.Fatal(err)
	}
	var landed liveplay.Event
	select {
	case landed = <-stream.Events():
		if landed.Kind != "song" {
			t.Fatalf("wrong event: %+v", landed)
		}
	default:
		t.Fatal("song did not start")
	}
	if err := transport.launchScene("main"); err != nil {
		t.Fatal(err)
	}
	if err := transport.launchPattern("bass", "pulse"); err != nil {
		t.Fatal(err)
	}
	// The UI receives the song event after the next requests were accepted.
	transport.markLanded(landed)
	state := transport.snapshot()
	if state.PendingScene != "main" || state.PendingSlots["bass"] != "pulse" {
		t.Fatalf("new requests were cleared by an old event: %+v", state)
	}
	// Old manual events must not clear a repeated request with the same name.
	transport.markLanded(liveplay.Event{Kind: "scene", Name: "main", Bar: 1})
	transport.markLanded(liveplay.Event{Kind: "slot", Track: "bass", Name: "pulse", Bar: 1})
	state = transport.snapshot()
	if state.PendingScene != "main" || state.PendingSlots["bass"] != "pulse" {
		t.Fatalf("delayed manual events cleared new requests: %+v", state)
	}
}

func TestSongLandingKeepsNewSongStartPending(t *testing.T) {
	_, path := studioTestHandler(t)
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.audioNull = true
	transport.stream, transport.playing = stream, true
	if err := transport.startFrom(0, "main", nil, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, stream, 8); err != nil {
		t.Fatal(err)
	}
	landed := <-stream.Events()
	if landed.Kind != "song" {
		t.Fatalf("wrong event: %+v", landed)
	}
	if err := transport.startFrom(0, "main", nil, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	transport.markLanded(landed)
	if state := transport.snapshot(); state.PendingSong != "main" {
		t.Fatalf("old song event cleared the next song request: %+v", state)
	}
	if _, err := io.CopyN(io.Discard, stream, 8); err != nil {
		t.Fatal(err)
	}
	if state := transport.snapshot(); state.PendingSong != "" {
		t.Fatalf("a consumed song request remains pending before event delivery: %+v", state)
	}
	transport.markLanded(<-stream.Events())
	if state := transport.snapshot(); state.PendingSong != "" {
		t.Fatalf("landed song request remained pending: %+v", state)
	}
}
