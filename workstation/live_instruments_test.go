package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/voice/keyboard"
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

	for _, want := range []string{`aria-label="Kick, General MIDI drum 36"`, `aria-label="Closed hat, General MIDI drum 42"`, `aria-label="C3, note 48, keyboard a"`, `aria-label="C♯3, note 49, keyboard w"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing accessible label %s", want)
		}
	}
	disclosure := regexp.MustCompile(`(?s)<details[^>]*data-live-midi-setup[^>]*>.*?</details>`).FindString(page)
	if disclosure == "" || strings.Contains(strings.SplitN(disclosure, ">", 2)[0], " open") || !strings.Contains(disclosure, "MIDI setup") {
		t.Error("MIDI setup must be closed by default")
	}
	if strings.Contains(disclosure, "Release notes") || !strings.Contains(page, "Release notes") {
		t.Error("Release notes must be outside MIDI setup")
	}
	for _, label := range []string{"Pitched track", "Pitched pattern", "Drum track", "Drum pattern"} {
		if !strings.Contains(page, `aria-label="`+label+`"`) {
			t.Errorf("missing accessible target name %s", label)
		}
	}
}

func TestGoSXLiveIncludesEveryModeledKeyboard(t *testing.T) {
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(fmt.Sprintf("cicada 2\ntrack keys %s { voices=4 }\ntrack drums drums {}\npattern n notes { c4 . }\nscene main { keys=n drums=off }\nsong { main }\n", name)))
			if len(ds) != 0 {
				t.Fatal(ds)
			}
			p, ds := project.FromScore(score)
			if len(ds) != 0 {
				t.Fatal(ds)
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
			if len(match) != 2 || !strings.Contains(match[1], `value="keys"`) || strings.Contains(match[1], `value="drums"`) {
				t.Fatal("modeled keys missing or drums offered as pitched target")
			}
		})
	}
}

func TestGoSXLiveWithoutDrumsHasNoPads(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntrack keys piano {}\npattern n notes { c4 . }\nscene main { keys=n }\nsong { main }\n"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", Valid: true, Project: p})
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
	if strings.Contains(page, "data-live-drum=") {
		t.Fatal("drum pads rendered without a drum track")
	}
}
