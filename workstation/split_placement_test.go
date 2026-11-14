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

	"m31labs.dev/cicada/project"
)

func TestSplitPlacementUsesShownTargetAndSourceReceipt(t *testing.T) {
	placement := project.Placement{ID: "verse", Track: "bass", Content: "pulse", AtTick: 0, LengthTicks: 7680}
	score := &project.Project{Edition: 2, Tracks: []project.Track{{ID: "bass", Kind: "acid"}}, Patterns: []project.Pattern{{ID: "pulse", Kind: "acid", Steps: 16}}, Arrange: &project.Arrangement{Placements: []project.Placement{placement}}}
	var calls int
	var intent map[string]any
	var receipt string
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: score})
		case "/api/intents":
			calls++
			receipt = r.Header.Get("X-Cicada-Source-Revision")
			_ = json.NewDecoder(r.Body).Decode(&intent)
			_, _ = io.WriteString(w, `{"revision":"next"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer audio.Close()
	backend, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := newApp(backend)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, server.URL+"/?panel=session&placement=verse")
	if !strings.Contains(page, `aria-label="Split position"`) || !strings.Contains(page, `value="3840"`) {
		t.Fatal("eligible split control missing")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"all-sources"}, "target_key": {placementTargetKey(placement)}, "name": {"verse"}, "at_tick": {"3840"}}
	response := post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode >= 400 || calls != 1 || receipt != "all-sources" {
		t.Fatalf("split: status=%d calls=%d receipt=%q", response.StatusCode, calls, receipt)
	}
	raw := intent["intents"].([]any)[0].(map[string]any)
	if intent["version"] != float64(1) || intent["revision"] != "current" || raw["kind"] != "splitclip" || raw["entity"] != "placement:verse" || raw["attick"] != float64(3840) {
		t.Fatalf("intent=%+v", intent)
	}
	score.Arrange.Placements[0].Content = "other"
	response = post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode != 409 || calls != 1 {
		t.Fatalf("replaced target status=%d calls=%d", response.StatusCode, calls)
	}
	form.Del("source_revision")
	response = post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode != 409 || calls != 1 {
		t.Fatalf("missing receipt status=%d calls=%d", response.StatusCode, calls)
	}
	form.Set("source_revision", "older-sources")
	response = post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode != 409 || calls != 1 {
		t.Fatalf("stale receipt status=%d calls=%d", response.StatusCode, calls)
	}
	form.Set("source_revision", "all-sources")
	score.Arrange.Placements[0] = placement
	form.Set("at_tick", "100")
	response = post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode != 422 || calls != 1 {
		t.Fatalf("off-boundary status=%d calls=%d", response.StatusCode, calls)
	}
	form.Set("at_tick", "3840")
	score.Tracks[0].Kind = "audio"
	response = post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode != 422 || calls != 1 {
		t.Fatalf("audio scope status=%d calls=%d", response.StatusCode, calls)
	}
	score.Tracks[0].Kind = "acid"
	score.Patterns[0].Performance = &project.Performance{}
	response = post(t, client, server.URL+"/__actions/split-placement", form, true, server.URL)
	if response.StatusCode != 422 || calls != 1 {
		t.Fatalf("performance scope status=%d calls=%d", response.StatusCode, calls)
	}
}

func TestSplitControlExcludesAudioAndShortOrPerformedPatterns(t *testing.T) {
	p := project.Placement{ID: "one", Track: "bass", Content: "p", LengthTicks: 3840}
	projectFor := func(pattern project.Pattern) *project.Project {
		return &project.Project{Edition: 2, Tracks: []project.Track{{ID: "bass", Kind: "acid"}}, Patterns: []project.Pattern{pattern}, Arrange: &project.Arrangement{Placements: []project.Placement{p}}}
	}
	for _, pattern := range []project.Pattern{{ID: "p", Steps: 16}, {ID: "p", Steps: 4, Performance: &project.Performance{}}, {ID: "other", Steps: 4}} {
		address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: projectFor(pattern)})
		if page := getPage(t, client, address+"/?panel=session"); strings.Contains(page, `/__actions/split-placement`) {
			t.Fatalf("unexpected split for %+v", pattern)
		}
	}
	audio := projectFor(project.Pattern{ID: "p", Steps: 4})
	audio.Tracks[0].Kind = "audio"
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: audio})
	if page := getPage(t, client, address+"/?panel=session"); strings.Contains(page, `/__actions/split-placement`) {
		t.Fatal("audio track must not offer pattern split, even with matching names")
	}

}

func TestSplitPickerBoundsAndExactOffGridPositions(t *testing.T) {
	p := &project.Project{Edition: 2,
		Tracks:   []project.Track{{ID: "bass", Kind: "acid"}},
		Patterns: []project.Pattern{{ID: "p", Steps: 1, StepTicks: 160}},
		Arrange:  &project.Arrangement{Placements: []project.Placement{{ID: "one", Track: "bass", Content: "p", LengthTicks: 160 * 130}}},
	}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=session")
	start := strings.Index(page, `name="at_tick"`)
	if start < 0 {
		t.Fatal("missing split picker")
	}
	end := strings.Index(page[start:], "</select>")
	if end < 0 {
		t.Fatal("unclosed split picker")
	}
	picker := page[start : start+end]
	if got := strings.Count(picker, "<option "); got != 128 {
		t.Fatalf("got %d boundaries, want 128", got)
	}
	for _, want := range []string{`value="160">@1.1.1 + 160 ticks`, `value="320">@1.1.2 + 80 ticks`, `value="20480"`} {
		if !strings.Contains(picker, want) {
			t.Errorf("missing exact boundary %s", want)
		}
	}
	if !strings.Contains(page, "Showing the first 128 boundaries.") {
		t.Fatal("missing bounded-list explanation")
	}
}
