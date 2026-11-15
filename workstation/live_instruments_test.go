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
	for _, label := range []string{"Release notes", "Enable MIDI"} {
		if strings.Contains(disclosure, label) || !strings.Contains(page, label) {
			t.Errorf("%s must be outside MIDI setup", label)
		}
	}
	for _, label := range []string{"Keys play: pitched track", "Keys play into: pitched pattern", "Pads play: drum track", "Pads play into: drum pattern"} {
		if !strings.Contains(page, `aria-label="`+label+`"`) {
			t.Errorf("missing accessible target name %s", label)
		}
	}
	header := regexp.MustCompile(`(?s)<div[^>]*class="live-input-heading"[^>]*>.*?</div>`).FindString(page)
	for _, want := range []string{`data-live-destination-pitched[^>]*>bass</span>`, `data-live-destination-drums[^>]*>drums</span>`, `data-live-midi-state[^>]*>MIDI off</span>`} {
		if !regexp.MustCompile(want).MatchString(header) {
			t.Errorf("missing header content %s", want)
		}
	}
	if !regexp.MustCompile(`<button\b[^>]*data-live-control="midi"[^>]*>Enable MIDI</button>`).MatchString(page) {
		t.Error("missing Enable MIDI button hook")
	}
	assertLiveTargets(t, page, true, true)
}

func assertLiveTargets(t *testing.T, page string, pitched, drums bool) {
	t.Helper()
	for _, group := range []struct {
		name, track, pattern string
		visible              bool
	}{
		{"Keys", "data-live-acid", "data-live-pattern", pitched},
		{"Pads", "data-live-drums", "data-live-drumpattern", drums},
	} {
		re := regexp.MustCompile(`(?s)<div\b([^>]*\blive-target-group\b[^>]*)>(.*?)</div>`)
		found := false
		for _, match := range re.FindAllStringSubmatch(page, -1) {
			if !strings.Contains(match[2], group.name+" play") {
				continue
			}
			found = true
			if strings.Contains(match[1], "hidden") == group.visible {
				t.Errorf("%s group visibility is wrong", group.name)
			}
			for _, hook := range []string{group.track, group.pattern} {
				if !regexp.MustCompile(`<select\b[^>]*\b` + hook + `(?:[= >])`).MatchString(match[2]) {
					t.Errorf("%s group missing select %s", group.name, hook)
				}
			}
		}
		if !found {
			t.Errorf("missing %s target group", group.name)
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
	assertLiveSingleTrack(t, "cicada 2\ntrack keys piano {}\npattern n notes { c4 . }\nscene main { keys=n }\nsong { main }\n", true)
}

func TestGoSXLiveWithoutPitchedHasNoKeys(t *testing.T) {
	assertLiveSingleTrack(t, "cicada 2\ntrack drums drums {}\npattern d drums { bd: x... }\nscene main { drums=d }\nsong { main }\n", false)
}

func assertLiveSingleTrack(t *testing.T, source string, pitched bool) {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
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
	assertLiveTargets(t, page, pitched, !pitched)
	if strings.Contains(page, "data-live-note=") != pitched || strings.Contains(page, "data-live-drum=") == pitched {
		t.Error("keys or pads do not match available tracks")
	}
}
