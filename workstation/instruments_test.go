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

	"golang.org/x/net/html"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestInstrumentLibraryUsesGoSXFormsAndShowsOverrides(t *testing.T) {
	patch, _ := instrument.FindPatch("warm-pad")
	declaration, _ := patch.Source("warm-pad")
	score, ds := notation.Parse([]byte("cicada 1\n" + declaration + "track warm-pad warm-pad { brightness = 1200.25Hz }\npattern p notes { c4 . }\nscene main { warm-pad=p }\nsong { main }\n"))
	if len(ds) != 0 {
		t.Fatalf("test score: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatalf("test project: %+v", ds)
	}
	var edits []map[string]any
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			if err := json.NewEncoder(w).Encode(workspace{Revision: "current", Valid: true, Project: p}); err != nil {
				t.Error(err)
			}
		case "/api/instrument":
			var edit map[string]any
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Error(err)
			}
			edits = append(edits, edit)
			if edit["revision"] != "current" {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"error":"score changed"}`)
			} else {
				_, _ = io.WriteString(w, `{"revision":"new","valid":true}`)
			}
		default:
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
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, app.URL+"/?panel=instruments")
	for _, text := range []string{"Instrument library", "Soft bell", "Round bass", "8-note polyphony", `action="/__actions/instrument"`, `name="newName" value="warm-pad-2"`, `name="track" value="warm-pad-2"`, "brightness · track override", `name="value" value="1200.25"`, "release · default", "1400ms", "Use default"} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing instrument UI %q", text)
		}
	}
	// Check actual forms: the authored shorthand expands during rendering.
	// A string in the navigation runtime must not satisfy this assertion.
	document, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var forms int
	var checkForms func(*html.Node)
	checkForms = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "form" {
			attrs := make(map[string]string)
			for _, attr := range node.Attr {
				attrs[attr.Key] = attr.Val
			}
			if attrs["action"] == "/__actions/instrument" {
				forms++
				if _, ok := attrs["data-gosx-form"]; !ok {
					t.Error("instrument form lacks the managed form contract")
				}
				for name, want := range map[string]string{"method": "post", "data-gosx-form-state": "idle", "data-gosx-enhance": "form", "data-gosx-enhance-layer": "bootstrap", "data-gosx-fallback": "native-form"} {
					if attrs[name] != want {
						t.Errorf("instrument form %s=%q, want %q", name, attrs[name], want)
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			checkForms(child)
		}
	}
	checkForms(document)
	if forms == 0 {
		t.Fatal("no instrument forms found")
	}
	csrf := tokenFromPage(t, page)
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"add-preset"}, "pattern": {"soft-bell"}, "newName": {"bell"}, "track": {"keys"}, "__gosx_return_to": {"/?panel=instruments"}}
	response := post(t, client, app.URL+"/__actions/instrument", form, false, app.URL)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/?panel=instruments" || len(edits) != 1 {
		t.Fatalf("native preset form: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	if edits[0]["pattern"] != "soft-bell" || edits[0]["newName"] != "bell" || edits[0]["track"] != "keys" || edits[0]["action"] != "add-preset" {
		t.Fatalf("preset payload: %+v", edits[0])
	}
	form.Set("action", "set-parameter")
	form.Set("track", "warm-pad")
	form.Set("newName", "brightness")
	form.Set("value", "1234.125")
	response = post(t, client, app.URL+"/__actions/instrument", form, false, app.URL)
	if response.StatusCode != 303 || len(edits) != 2 || edits[1]["value"] != "1234.125" || edits[1]["newName"] != "brightness" {
		t.Fatalf("typed instrument parameter payload: %d %+v", response.StatusCode, edits)
	}
	form.Set("action", "reset-parameter")
	response = post(t, client, app.URL+"/__actions/instrument", form, true, app.URL)
	var result struct {
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 303 || result.Redirect != "/?panel=instruments" || len(edits) != 3 || edits[2]["action"] != "reset-parameter" {
		t.Fatalf("reset instrument action: %d %+v", response.StatusCode, edits)
	}
	form.Set("revision", "old")
	if stale := post(t, client, app.URL+"/__actions/instrument", form, true, app.URL); stale.StatusCode != 409 {
		t.Fatalf("stale preset: %d", stale.StatusCode)
	}
}
