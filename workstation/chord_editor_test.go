package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestChordVoicingsAndMIDIBounds(t *testing.T) {
	for _, test := range []struct {
		root, octave int
		shape        string
		inversion    int
		want         []int
	}{
		{9, 3, "minor", 0, []int{57, 60, 64}},
		{0, 4, "major", 1, []int{64, 67, 72}},
		{2, 3, "minor7", 3, []int{60, 62, 65, 69}},
		{0, -1, "sus2", 0, []int{0, 2, 7}},
		{0, 9, "major", 0, []int{120, 124, 127}},
	} {
		got, err := chordPitches(test.root, test.octave, test.shape, test.inversion)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("voicing %+v: %v %v", test, got, err)
		}
	}
	for _, test := range []struct {
		root, octave int
		shape        string
		inversion    int
	}{
		{12, 3, "major", 0}, {0, -2, "major", 0}, {11, 8, "major7", 0}, {0, 3, "major", 3}, {0, 3, "unknown", 0},
	} {
		if _, err := chordPitches(test.root, test.octave, test.shape, test.inversion); err == nil {
			t.Fatalf("invalid voicing accepted: %+v", test)
		}
	}
}

func TestChordActionMapsMusicalChoicesToAtomicVoicing(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"chord"}, "pattern": {"harmony"}, "step": {"3"}, "octave": {"3"}, "chord_root": {"9"}, "chord_octave": {"3"}, "chord_shape": {"minor"}, "chord_inversion": {"0"}, "hold": {"4"}}
	response := post(t, client, address+"/__actions/pattern", form, false, address)
	target, _ := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != 303 || target.Query().Get("pattern") != "harmony" || target.Query().Get("step") != "3" || len(*edits) != 1 {
		t.Fatalf("chord action: %d target=%v edits=%v", response.StatusCode, target, *edits)
	}
	chord := (*edits)[0]["chord"].(map[string]any)
	if (*edits)[0]["step"] != float64(2) || chord["hold"] != float64(4) || !reflect.DeepEqual(chord["notes"], []any{float64(57), float64(60), float64(64)}) {
		t.Fatalf("voicing payload: %+v", *edits)
	}
	form.Set("chord_inversion", "3")
	if response := post(t, client, address+"/__actions/pattern", form, true, address); response.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("invalid inversion reached the backend")
	}
	form.Set("chord_inversion", "0")
	form.Set("revision", "stale")
	if response := post(t, client, address+"/__actions/pattern", form, true, address); response.StatusCode != 409 {
		t.Fatal("chord creation ignored the score revision")
	}
}

func TestChordGridDisplaysAllVoicesAndNativeFormRetainsErrors(t *testing.T) {
	data := make([]*project.Step, 16)
	data[0] = &project.Step{Note: 57, Notes: []int{57, 60, 64}, Ratchet: 1, Probability: 100, Velocity: 100}
	data[1] = &project.Step{Tie: true, Ratchet: 1, Probability: 100}
	p := &project.Project{Title: "Harmony", TempoMilli: 130000, Key: project.Key{Root: 9, Scale: "minor"}, Patterns: []project.Pattern{{ID: "harmony", Kind: "notes", Steps: 16, Data: data}}}
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
	page := getPage(t, client, app.URL+"/?panel=patterns&pattern=harmony&step=1")
	for _, name := range []string{"A3", "C4", "E4"} {
		label := `aria-label="harmony step 1 ` + name + `" aria-pressed="true"`
		if !strings.Contains(page, label) {
			t.Fatalf("chord pitch %s is invisible", name)
		}
	}
	for _, text := range []string{"Build a chord", "Current: A3 · C4 · E4", `name="hold" value="2"`, "First pitch (moves whole voicing)", `name="chord_root"`, `value="minor" selected`} {
		if !strings.Contains(page, text) {
			t.Fatalf("chord editor missing %q", text)
		}
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "action": {"chord"}, "pattern": {"harmony"}, "step": {"1"}, "chord_root": {"9"}, "chord_octave": {"3"}, "chord_shape": {"minor"}, "chord_inversion": {"0"}, "hold": {"66"}, "__gosx_return_to": {"/?panel=patterns&pattern=harmony&step=1"}}
	response := post(t, client, app.URL+"/__actions/pattern", form, false, app.URL)
	page = getPage(t, client, app.URL+response.Header.Get("Location"))
	if response.StatusCode != 303 || !strings.Contains(page, `name="hold" value="66"`) || !strings.Contains(page, "hold must be 1–64 steps") {
		t.Fatal("native chord validation lost the attempted voicing")
	}
}
