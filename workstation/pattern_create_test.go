package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestEmptyPatternLibraryOffersCreationAndRetainsNativeErrors(t *testing.T) {
	// Audio-only arrangements can have no note patterns. A newly added
	// instrument still needs to be able to create its first grid here.
	p := &project.Project{Title: "First pattern", TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "tine_ep"}, {ID: "vox", Kind: "audio"}}, Scenes: []project.Scene{{ID: "main"}}}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", Valid: true, Project: p})
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer audio.Close()
	b, _ := newBackend(audio.URL)
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, app.URL+"/?panel=patterns")
	for _, text := range []string{"Create your first pattern", `action="/__actions/pattern"`, `name="action" value="create"`, "keys · notes", "Library only", `name="length" value="16"`} {
		if !strings.Contains(page, text) {
			t.Fatalf("empty pattern library is missing %q", text)
		}
	}
	if strings.Contains(page, `<option value="vox"`) {
		t.Fatal("audio tracks were offered as note pattern destinations")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "action": {"create"}, "createFor": {""}, "newName": {"my-melody"}, "track": {"keys"}, "scene": {"main"}, "length": {"66"}, "starter": {"arpeggio"}, "__gosx_return_to": {"/?panel=patterns"}}
	response := post(t, client, app.URL+"/__actions/pattern", form, false, app.URL)
	if response.StatusCode != 303 {
		t.Fatalf("native validation did not redirect: %d", response.StatusCode)
	}
	page = getPage(t, client, app.URL+response.Header.Get("Location"))
	for _, text := range []string{`name="newName" value="my-melody"`, `name="length" value="66"`, `value="arpeggio" selected`, "pattern length must be 1–64 steps"} {
		if !strings.Contains(page, text) {
			t.Fatalf("native validation lost %q", text)
		}
	}
}

func TestPatternStartingIdeasFitSoundAndReachTheDomain(t *testing.T) {
	score, diagnostics := notation.Parse([]byte("cicada 2\ntitle \"Write a part\"\ninstrument polyvoice { voice poly { out=sine(pitch) } }\ninstrument monovoice { voice mono { out=sine(pitch) } }\ntrack keys polyvoice {}\ntrack low monovoice {}\ntrack drums drums {}\npattern original notes { c4 . }\nscene main {}\nsong { main }\n"))
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=instruments")
	for _, check := range []struct{ track, present, absent string }{
		{"keys", `value="chords"`, `value="backbeat"`},
		{"low", `value="arpeggio"`, `value="chords"`},
	} {
		start := strings.Index(page, `data-gosx-key="create-pattern-`+check.track+`"`)
		if start < 0 {
			t.Fatal("missing track creation form for " + check.track)
		}
		form := page[start:]
		form = form[:strings.Index(form, "</details>")]
		if !strings.Contains(form, check.present) || strings.Contains(form, check.absent) {
			t.Fatalf("incompatible starting ideas for %s", check.track)
		}
	}
	library := getPage(t, client, address+"/?panel=patterns")
	if !strings.Contains(library, `value="backbeat"`) || !strings.Contains(library, "drums · drums") {
		t.Fatal("pattern library does not offer a drum starting idea")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "action": {"create"}, "createFor": {"keys"}, "newName": {"harmony"}, "track": {"keys"}, "scene": {"main"}, "length": {"32"}, "starter": {"chords"}, "__gosx_return_to": {"/?panel=instruments"}}
	response := post(t, client, address+"/__actions/pattern", form, false, address)
	defer response.Body.Close()
	if response.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["starter"] != "chords" || (*edits)[0]["length"] != float64(32) || (*edits)[0]["track"] != "keys" {
		t.Fatalf("starting idea lost before domain edit: %d %+v", response.StatusCode, *edits)
	}
}
