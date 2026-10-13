package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAutomationActionKeepsNativeReturnTargetUnitsAndRevision(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"automation-set"}, "scene": {"main"}, "path": {"bass.cutoff"}, "value": {"1.25kHz"}, "__gosx_return_to": {automationURL("bass.cutoff")}}
	response := post(t, client, address+"/__actions/automation", form, false, address)
	if response.StatusCode != 303 || response.Header.Get("Location") != automationURL("bass.cutoff") || len(*edits) != 1 {
		t.Fatalf("native point: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	if (*edits)[0]["value"] != "1.25kHz" || (*edits)[0]["revision"] != "current" {
		t.Fatalf("point payload: %+v", *edits)
	}
	form.Set("revision", "old")
	response = post(t, client, address+"/__actions/automation", form, true, address)
	if response.StatusCode != 409 {
		t.Fatal("stale automation point accepted")
	}
}

func TestAutomationLanesShowPointCarryAndCatalogChoices(t *testing.T) {
	source := []byte("tempo 120\ntrack bass acid { cutoff = 700Hz }\npattern riff acid steps=2 { 1 . }\nscene main { bass = riff bass.cutoff = 900Hz }\nscene hold { bass = keep }\nsong { main hold main }\n")
	score, _ := notation.Parse(source)
	p, ds := project.FromScore(score)
	if p == nil || len(ds) > 0 {
		t.Fatalf("test score: %+v", ds)
	}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Source: string(source), Revision: "current", Valid: true, Project: p})
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
	page := getPage(t, app.Client(), app.URL+automationURL("bass.cutoff"))
	for _, text := range []string{`data-automation-lane="bass.cutoff"`, "● 900Hz", "↳ 900Hz", "Bar 2 · hold", `name="path" value="bass.cutoff"`, "bass.cutoff accepts", "Remove point"} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing %q", text)
		}
	}
	if strings.Contains(page, `<option value="bass.insert"`) {
		t.Fatal("unimplemented automation offered")
	}
	// The scene tempo is a scene setting, offered with its range; it is not a
	// continuous automation curve.
	if !strings.Contains(page, `<option value="tempo"`) {
		t.Fatal("the scene tempo is not offered")
	}
}

// A scene's tempo can be added, changed and removed from the scene points
// panel. The forms post the same automation actions as every other point, so
// the edit service applies and undoes them.
func TestSceneTempoControlsAddChangeAndRemove(t *testing.T) {
	source := []byte("tempo 120\ntrack bass acid { cutoff = 700Hz }\npattern riff acid steps=2 { 1 . }\nscene main { bass = riff }\nscene slow { bass = riff tempo = 84 }\nsong { main slow }\n")
	score, _ := notation.Parse(source)
	p, ds := project.FromScore(score)
	if p == nil || len(ds) > 0 {
		t.Fatalf("test score: %+v", ds)
	}
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Source: string(source), Revision: "current", Valid: true, Project: p})
		} else {
			_, _ = io.WriteString(w, `{"revision":"current","valid":true}`)
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
	page := getPage(t, app.Client(), app.URL+automationURL("tempo"))
	for _, text := range []string{
		`<option value="tempo"`, `data-automation-lane="tempo"`, "● 84", "↳ 120", "Bar 2 · slow",
		"tempo accepts 20 to 300 BPM",
		`name="path" value="tempo"`,          // the add form and the change form post the path
		"Add or replace point", "Save point", // add and change
		"Remove point",
	} {
		if !strings.Contains(page, text) {
			t.Errorf("scene tempo controls lack %q", text)
		}
	}
	// The add form starts from the score tempo.
	if !strings.Contains(page, `value="120"`) {
		t.Error("the add form does not start from the score tempo")
	}
	// Adding to a scene and changing a scene's tempo post automation-set; the
	// backend (the edit service, with undo) applies them.
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	for _, c := range []struct{ scene, value string }{{"main", "100"}, {"slow", "66.5"}} {
		form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"automation-set"}, "scene": {c.scene}, "path": {"tempo"}, "value": {c.value}, "__gosx_return_to": {automationURL("tempo")}}
		if response := post(t, client, address+"/__actions/automation", form, false, address); response.StatusCode != 303 {
			t.Fatalf("set tempo in %s: %d", c.scene, response.StatusCode)
		}
	}
	remove := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"automation-remove"}, "scene": {"slow"}, "path": {"tempo"}, "__gosx_return_to": {automationURL("tempo")}}
	if response := post(t, client, address+"/__actions/automation", remove, false, address); response.StatusCode != 303 {
		t.Fatalf("remove tempo: %d", response.StatusCode)
	}
	if len(*edits) != 3 || (*edits)[0]["path"] != "tempo" || (*edits)[0]["value"] != "100" || (*edits)[1]["value"] != "66.5" || (*edits)[2]["action"] != "automation-remove" {
		t.Fatalf("posted edits: %+v", *edits)
	}
}
