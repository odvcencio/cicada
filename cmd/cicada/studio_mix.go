package main

import (
	_ "embed"
	"net/http"
)

//go:embed studio-mix.js
var studioMixScript []byte

func (s *studio) mixScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(studioMixScript)
}
