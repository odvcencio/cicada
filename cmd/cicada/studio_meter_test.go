package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

const studioWaltz = `cicada 2
title "Waltz"
tempo 120
meter 3/4
track bass acid {}
pattern riff { c3 . c4 . }
arrange {
  tempo @1.1.1 120
  place verse bass riff { at = @1.1.1 length = 4bars }
  marker chorus { at = @3.1.1 }
}
`

// Studio opens a song in 3/4: every read route answers without a server
// error, and the edit service writes positions on the song's meter. Studio's
// own views and gestures still draw a 4/4 grid until time-signatures phase 5.
func TestStudioOpensASongInThreeFour(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), "waltz.cicada")
	if err := os.WriteFile(path, []byte(studioWaltz), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/", "/api/state", "/api/workspace", "/api/files", "/api/revision", "/api/meters", "/api/takes", "/api/capture", "/api/instrument-state", "/api/mixer", "/api/transport", "/api/params", "/api/audio/config", "/api/export", "/api/history", "/api/library", "/api/collaboration/store"} {
		if r := studioCall(t, h, route, nil); r.Code >= 500 {
			t.Errorf("%s: %d %s", route, r.Code, r.Body.String())
		}
	}
	body := `{"version":1,"revision":"` + edits.Revision([]byte(studioWaltz)) + `","intents":[{"kind":"addmarker","name":"drop","settings":{"at":"@2.3.1"}}]}`
	if r := studioCallRaw(t, h, "/api/intents", body); r.Code != http.StatusOK {
		t.Fatalf("intent in 3/4: %d %s", r.Code, r.Body.String())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "marker drop { at = @2.3.1 }") {
		t.Fatalf("the 3/4 position was not written:\n%s", data)
	}
	body = `{"version":1,"revision":"` + edits.Revision(data) + `","intents":[{"kind":"addmarker","name":"late","settings":{"at":"@2.4.1"}}]}`
	if r := studioCallRaw(t, h, "/api/intents", body); r.Code == http.StatusOK || !strings.Contains(r.Body.String(), "which is in 3/4") {
		t.Fatalf("a fourth beat in 3/4: %d %s", r.Code, r.Body.String())
	}
}
