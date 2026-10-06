package main

import (
	"encoding/json"
	"io"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

func TestStudioExpressionMessagesValidateAndReachLiveplay(t *testing.T) {
	path := studioTestPath(t, studioScore)
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s := &studio{path: path, transport: &studioTransport{stream: stream, playing: true, audioNull: true}}
	apply := func(raw string) error {
		var message audioClientMessage
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			return err
		}
		return s.applyAudioMessage(message)
	}
	for _, raw := range []string{
		`{"type":"note","track":"bass","note":60,"velocity":100,"on":true,"noteId":42,"channel":1}`,
		`{"type":"note-expression","track":"bass","noteId":42,"channel":1,"pitchCents":25.125,"pressure":0.500001,"timbre":0.75}`,
		`{"type":"note","track":"bass","note":60,"velocity":0,"on":false,"noteId":42,"channel":1}`,
	} {
		if err := apply(raw); err != nil {
			t.Fatalf("valid expression path: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"type":"note-expression","track":"bass","pitchCents":0,"pressure":0,"timbre":0.5}`,
		`{"type":"note-expression","track":"bass","noteId":0,"pitchCents":0,"pressure":0,"timbre":0.5}`,
		`{"type":"note-expression","track":"bass","noteId":65535,"pitchCents":0,"pressure":0,"timbre":0.5}`,
		`{"type":"note-expression","track":"bass","noteId":1,"pitchCents":9601,"pressure":0,"timbre":0.5}`,
		`{"type":"note-expression","track":"bass","noteId":1,"pitchCents":0,"pressure":-0.1,"timbre":0.5}`,
		`{"type":"note-expression","track":"bass","noteId":1,"pitchCents":0,"pressure":0,"timbre":1.1}`,
		`{"type":"note-expression","track":"bass","noteId":1,"channel":16,"pitchCents":0,"pressure":0,"timbre":0.5}`,
		`{"type":"note-expression","track":"drums","noteId":1,"pitchCents":0,"pressure":0,"timbre":0.5}`,
		`{"type":"note-expression","track":"bass","noteId":1,"pitchCents":0,"pressure":0}`,
	} {
		if err := apply(raw); err == nil {
			t.Fatalf("accepted malformed expression %s", raw)
		}
	}
	if _, err := io.CopyN(io.Discard, stream, int64(liveBlockFrames*8)); err != nil {
		t.Fatal(err)
	}
}
