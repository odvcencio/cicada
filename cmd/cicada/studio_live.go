package main

import (
	_ "embed"
	"net/http"
)

//go:embed studio-midi.js
var studioMIDIScript []byte

//go:embed studio-live.js
var studioLiveScript []byte

func (s *studio) midiScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioMIDIScript)
}

func (s *studio) liveScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioLiveScript)
}
