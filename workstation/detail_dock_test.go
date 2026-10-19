package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func detailDockApp(t *testing.T) (string, *http.Client, *[]map[string]any) {
	t.Helper()
	patch, _ := instrument.FindPatch("warm-pad")
	declaration, _ := patch.Source("warm-pad")
	source := "cicada 2\n" + declaration + "track bass warm-pad { brightness = 1200.25Hz }\ntrack lead acid {}\npattern p notes { c4 . }\nscene main { bass=p }\nsong { main*4 }\n"
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p.Song = nil
	p.Effects = append(p.Effects, project.Effect{ID: "tone", Kind: "eq", Params: map[string]project.Value{}})
	p.Arrange = &project.Arrangement{Placements: []project.Placement{{ID: "bass-intro", Track: "bass", Content: "p", LengthTicks: 15360}, {ID: "lead-intro", Track: "lead", Content: "p", LengthTicks: 15360}}}
	var mu sync.Mutex
	revision := "current"
	var edits []map[string]any
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Source: source, Filename: "test.cicada", Revision: revision, Edition: 2, Valid: true, Project: p})
		case "/api/mixer":
			if r.Method == http.MethodGet {
				state := mixerState{Edition: 2, Revision: revision, Effects: []mixerReturn{{Name: "tone", Fields: []mixerField{{Path: "tone.hp", Value: 120.0, Supported: true, Descriptor: paramdefs.Descriptor{Unit: "Hz", Min: 20, Max: 20000}}}}}}
				for _, track := range p.Tracks {
					state.Tracks = append(state.Tracks, mixerStrip{ID: track.ID, Name: track.ID, Inserts: []string{"tone"}, Fields: map[string]mixerField{"insert": {Path: track.ID + ".insert", Value: "tone", Supported: true}, "pan": {Path: track.ID + ".pan", Value: 0.0, Supported: true, Descriptor: paramdefs.Descriptor{Min: -1, Max: 1}}}})
				}
				_ = json.NewEncoder(w).Encode(state)
				return
			}
			fallthrough
		case "/api/instrument", "/api/automation":
			var edit map[string]any
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Error(err)
			}
			if edit["revision"] != revision {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"error":"score changed"}`)
				return
			}
			edits = append(edits, edit)
			revision = "after"
			_, _ = io.WriteString(w, `{"revision":"after","valid":true}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(audio.Close)
	backend, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(backend)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return app.URL, client, &edits
}

func TestShellDeviceFollowsPlacementAndSharesWorkingParameterControls(t *testing.T) {
	address, client, edits := detailDockApp(t)
	location := "/studio?placement=bass-intro"
	body := getPage(t, client, address+location)
	for _, want := range []string{`id="device-editor"`, `data-device-track="bass"`, `name="deviceTrack"`, "Inspect track", "brightness · track override", `name="value" value="1200.25"`, `name="path" value="bass.pan"`, "Use default", "Insert effects", `id="device-effect-tone"`, `href="#device-effect-tone"`, `name="path" value="tone.hp"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Device lacks %q", want)
		}
	}
	returnTo := "/studio?deviceTrack=bass&dock=device&placement=bass-intro"
	if !strings.Contains(body, `name="__gosx_return_to" value="/studio?deviceTrack=bass&amp;dock=device&amp;placement=bass-intro"`) {
		t.Error("hidden Device forms must return to Device")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, body)}, "revision": {"current"}, "path": {"bass.pan"}, "value": {"0.25"}, "valueType": {"number"}, "__gosx_return_to": {returnTo}}
	response := shellPost(t, client, address, "/__actions/mixer", form, address+location)
	if response.StatusCode != 303 || response.Header.Get("Location") != returnTo || len(*edits) != 1 || (*edits)[0]["path"] != "bass.pan" {
		t.Fatalf("Device form: status=%d return=%s edits=%+v", response.StatusCode, response.Header.Get("Location"), *edits)
	}
	body = getPage(t, client, address+returnTo)
	if !strings.Contains(body, `name="revision" value="after"`) || strings.Contains(body, `name="revision" value="current"`) {
		t.Error("Device refresh kept stale action revisions")
	}
	for _, query := range []string{"?dock=device&placement=lead-intro", "?dock=device&deviceTrack=lead&placement=bass-intro"} {
		if body := getPage(t, client, address+"/studio"+query); !strings.Contains(body, `data-device-track="lead"`) {
			t.Errorf("Device selection lost for %s", query)
		}
	}
}

func TestShellAutomationLivesInOneDetailDockAndKeepsParameterContext(t *testing.T) {
	address, client, _ := detailDockApp(t)
	body := getPage(t, client, address+"/studio?tab=session&curvePath=bass.pan")
	for _, id := range []string{"automation-editor", "automation", "cicada-automation-curve"} {
		if count := strings.Count(body, `id="`+id+`"`); count != 1 {
			t.Errorf("%s rendered %d times", id, count)
		}
	}
	seen := map[string]bool{}
	for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if seen[match[1]] && !strings.HasPrefix(match[1], "source-editor") {
			t.Errorf("duplicate ID %q with all panels mounted", match[1])
		}
		seen[match[1]] = true
	}
	start := strings.Index(body, `id="detail-panel-automation"`)
	if start < 0 {
		t.Fatal("missing Automation dock")
	}
	dock := body[start:]
	for _, want := range []string{`data-automation-curve`, `name="curvePath"`, `action="/studio"`, `name="dock" value="automation"`, `name="__gosx_return_to" value="/studio?curvePath=bass.pan&amp;dock=automation&amp;tab=session"`} {
		if !strings.Contains(dock, want) {
			t.Errorf("Automation dock lacks %q", want)
		}
	}
	legacy := getPage(t, client, address+"/?panel=session&curvePath=bass.pan")
	if !strings.Contains(legacy, `id="automation-editor"`) || !strings.Contains(legacy, `id="automation"`) {
		t.Error("standalone Session lost automation")
	}
}

func TestDeviceTrackFallbackAndEmptyDock(t *testing.T) {
	p := &project.Project{Tracks: []project.Track{{ID: "bass"}, {ID: "lead"}}}
	if deviceTrack(p, url.Values{"deviceTrack": {"missing"}}).ID != "bass" || deviceTrack(nil, nil) != nil {
		t.Error("unsafe track fallback")
	}
	body := shellBody(t, "?dock=device")
	for _, want := range []string{`id="device-editor"`, "Add an instrument in the Browser", `id="automation-empty"`} {
		if !strings.Contains(body, want) {
			t.Errorf("empty dock lacks %q", want)
		}
	}
	node := gosx.El("form", hidden("__gosx_return_to", "/?panel=mixer"))
	result := gosx.RenderHTML(dockForms(node, `/studio?file=a"<b>&dock=device`))
	if strings.Contains(result, `file=a"<b>`) || !strings.Contains(result, "&lt;b&gt;") {
		t.Error("return target not escaped")
	}
}
