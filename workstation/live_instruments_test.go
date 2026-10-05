package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGoSXLiveIncludesAuthoredPitchedTargets(t *testing.T) {
	score, ds := notation.Parse([]byte(`cicada 2
instrument mono-voice { voice mono { out = sine(pitch) } }
instrument poly-voice { voice poly { out = sine(pitch) } }
track bass acid {}
track mono mono-voice {}
track keys poly-voice {}
track drums drums {}
track audio audio {}
pattern a acid { 1 . }
pattern n notes { c4 . }
pattern d drums { bd: x... }
scene main { bass=a mono=n keys=n drums=d audio=off }
song { main }
`))
	if len(ds) != 0 {
		t.Fatalf("test score: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatalf("test project: %+v", ds)
	}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/workspace" {
			if err := json.NewEncoder(w).Encode(workspace{Revision: "current", Valid: true, Project: p}); err != nil {
				t.Error(err)
			}
		} else {
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer audio.Close()
	b, _ := newBackend(audio.URL)
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	page := getPage(t, app.Client(), app.URL+"/?panel=live")
	match := regexp.MustCompile(`(?s)<select[^>]*data-live-acid[^>]*>(.*?)</select>`).FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("pitched Go/WASM selector is absent")
	}
	for _, track := range []string{"bass", "mono", "keys"} {
		if !strings.Contains(match[1], `value="`+track+`"`) {
			t.Fatalf("authored target %s missing", track)
		}
	}
	for _, track := range []string{"drums", "audio"} {
		if strings.Contains(match[1], `value="`+track+`"`) {
			t.Fatalf("non-pitched target %s offered as keys", track)
		}
	}
	if !strings.Contains(page, "Pitched track") || !strings.Contains(page, "Pitched pattern") {
		t.Fatal("live labels still imply only acid is playable")
	}
}
