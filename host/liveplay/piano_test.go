package liveplay

import (
	"encoding/binary"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

func TestPianoLiveChordsAndKeyRange(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 8, BPMMilli: 120_000}
	cfg.Track[0].Kind = engine.VoicePiano
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "grand", Kind: "piano"}}}, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []int{20, 109} {
		if err := p.Note("grand", key, 100, true); err == nil {
			t.Fatalf("piano host accepted out-of-range key %d", key)
		}
	}
	for _, key := range []int{60, 64, 67} {
		if err := p.Note("grand", key, 100, true); err != nil {
			t.Fatal(err)
		}
	}
	pcm := make([]byte, blockFrames*8)
	for range 8 {
		if _, err := p.Read(pcm); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Note("grand", 64, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(pcm); err != nil {
		t.Fatal(err)
	}
	noteOns, noteOffs := 0, 0
	for len(p.events) > 0 {
		event := <-p.events
		if event.Kind == "note-on" {
			noteOns++
		} else if event.Kind == "note-off" {
			noteOffs++
		} else if event.Kind == "note-error" {
			t.Fatalf("piano live command rejected: %+v", event)
		}
	}
	var energy float64
	for at := 0; at+4 <= len(pcm); at += 4 {
		sample := math.Float32frombits(binary.LittleEndian.Uint32(pcm[at:]))
		energy += float64(sample) * float64(sample)
	}
	if p.fault != nil || noteOns != 3 || noteOffs != 1 || energy <= 0 {
		t.Fatalf("piano host chord delivery: fault=%v note-ons=%d note-offs=%d energy=%g", p.fault, noteOns, noteOffs, energy)
	}
}
