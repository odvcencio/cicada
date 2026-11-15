package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
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
	return detailDockAppWithMaster(t, mixerStrip{})
}

func detailDockAppWithMaster(t *testing.T, master mixerStrip) (string, *http.Client, *[]map[string]any) {
	t.Helper()
	patch, _ := instrument.FindPatch("warm-pad")
	declaration, _ := patch.Source("warm-pad")
	source := "cicada 2\n" + declaration + "track bass warm-pad { brightness = 1200.25Hz }\ntrack lead acid {}\ntrack beat drums { bd_decay = 400ms }\npattern p notes { c4 . }\nscene main { bass=p }\nsong { main*4 }\n"
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
				state := mixerState{Edition: 2, Revision: revision, Master: master, Effects: []mixerReturn{{Name: "tone", Fields: []mixerField{{Path: "tone.hp", Value: 120.0, Supported: true, Descriptor: paramdefs.Descriptor{Unit: "Hz", Min: 20, Max: 20000}}}}}}
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

func TestMasterInsertNoteOnPanelAndDevice(t *testing.T) {
	for _, count := range []int{1, 16} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			master := mixerStrip{ID: "master", Name: "master", Inserts: make([]string, count), Fields: map[string]mixerField{
				"insert": {Reason: "CICADA-UNSUPPORTED: master inserts are not implemented"},
			}}
			address, client, _ := detailDockAppWithMaster(t, master)
			panel := getPage(t, client, address+"/?panel=mixer")
			if !strings.Contains(panel, "Master inserts are not implemented.") {
				t.Fatal("legacy mixer lost the master insert explanation")
			}
			device := getPage(t, client, address+"/studio?dock=device&deviceTrack=master")
			// The master's channel reference is "master:master"; its address is "master".
			start := strings.Index(device, `data-device-channel="master:master"`)
			if start < 0 {
				t.Fatal("master Device was not selected")
			}
			device = device[start:]
			if end := strings.Index(device, `aria-label="Insert effects"`); end >= 0 {
				device = device[end:]
				if end = strings.Index(device, "</section>"); end >= 0 {
					device = device[:end]
				}
			} else {
				t.Fatal("Device lacks the insert effects group")
			}
			if strings.Contains(device, "Master inserts are not implemented.") {
				t.Fatal("Device shows the master insert explanation")
			}
			want := "Add insert"
			if count == 16 {
				want = "This chain is full."
			}
			if !strings.Contains(device, want) {
				t.Fatalf("Device missing %q", want)
			}
		})
	}
}

func TestShellDeviceFollowsPlacementAndSharesWorkingParameterControls(t *testing.T) {
	address, client, edits := detailDockApp(t)
	location := "/studio?placement=bass-intro"
	body := getPage(t, client, address+location)
	for _, want := range []string{`id="device-editor"`, `data-device-track="bass"`, `data-device-channel="bass"`, `name="deviceTrack"`, "Inspect channel", `<option value="bass" selected>bass</option>`, "brightness · track override", `name="value" value="1200.25"`, `name="path" value="bass.pan"`, "Use default", "Insert effects", `id="device-effect-tone"`, `href="#device-effect-tone"`, `name="path" value="tone.hp"`} {
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

func TestDeviceBuiltinSoundControlsUseCatalogAndSavedActions(t *testing.T) {
	address, client, edits := detailDockApp(t)
	location := "/studio?dock=device&placement=lead-intro"
	body := getPage(t, client, address+location)
	parameter := func(body, path string) string {
		return regexp.MustCompile(`(?s)<div[^>]*data-device-parameter="` + regexp.QuoteMeta(path) + `"[^>]*>.*?</form>.*?</div>`).FindString(body)
	}
	order := regexp.MustCompile(`data-device-parameter="lead\.([^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(order) < 5 || order[0][1] != "cutoff" || order[1][1] != "reso" {
		t.Fatalf("acid's first visible controls must shape its tone: %+v", order)
	}
	last := order[len(order)-3:]
	if last[0][1] != "tune" || last[1][1] != "fine" || last[2][1] != "octave" {
		t.Errorf("shared pitch controls must follow the voice's sound settings: %+v", last)
	}
	cutoff := parameter(body, "lead.cutoff")
	for _, want := range []string{`action="/__actions/instrument"`, `name="track" value="lead"`, `name="newName" value="cutoff"`, `name="value" value="600"`, `min="20"`, `max="8000"`, `step="any"`, "cutoff · default (Hz)", "20Hz to 8000Hz · Default: 600Hz", "Apply", "Use default"} {
		if !strings.Contains(cutoff, want) {
			t.Errorf("built-in cutoff control missing %q: %s", want, cutoff)
		}
	}
	for path, choices := range map[string][]string{"lead.filter": {"diode", "ladder"}, "lead.savage": {"off", "on"}, "lead.octave": {"0", "1", "2", "3", "4", "5", "6"}} {
		row := parameter(body, path)
		for _, choice := range choices {
			if !strings.Contains(row, `<option value="`+choice+`"`) {
				t.Errorf("%s missing choice %s", path, choice)
			}
		}
	}
	for _, name := range []string{"level", "pan", "mute", "solo", "out", "insert"} {
		if strings.Contains(body, `data-device-parameter="lead.`+name+`"`) {
			t.Errorf("mixer setting %s leaked into Sound", name)
		}
	}
	returnTo := "/studio?deviceTrack=lead&dock=device&placement=lead-intro"
	form := url.Values{"csrf_token": {tokenFromPage(t, body)}, "revision": {"current"}, "action": {"set-parameter"}, "track": {"lead"}, "newName": {"cutoff"}, "value": {"1250.125"}, "__gosx_return_to": {returnTo}}
	response := shellPost(t, client, address, "/__actions/instrument", form, address+location)
	if response.StatusCode != 303 || response.Header.Get("Location") != returnTo || len(*edits) != 1 {
		t.Fatalf("builtin sound POST status=%d location=%s edits=%+v", response.StatusCode, response.Header.Get("Location"), *edits)
	}
	edit := (*edits)[0]
	for key, want := range map[string]string{"revision": "current", "action": "set-parameter", "track": "lead", "newName": "cutoff", "value": "1250.125"} {
		if edit[key] != want {
			t.Errorf("builtin sound payload %s=%v want %s", key, edit[key], want)
		}
	}
	body = getPage(t, client, address+returnTo)
	if !strings.Contains(body, `data-device-track="lead"`) || !strings.Contains(parameter(body, "lead.cutoff"), `name="revision" value="after"`) || strings.Contains(body, `name="revision" value="current"`) {
		t.Error("saved builtin control lost selected track or kept stale forms")
	}
	body = getPage(t, client, address+"/studio?dock=device&deviceTrack=beat")
	drumOrder := regexp.MustCompile(`data-device-parameter="beat\.([^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(drumOrder) < 2 || drumOrder[0][1] != "bd_tune" || drumOrder[1][1] != "bd_decay" {
		t.Errorf("drum-specific tuning and decay must stay at the front: %+v", drumOrder)
	}
	for path, texts := range map[string][]string{"beat.bd_tune": {"bd_tune · default (Hz)", `name="value" value="55"`, "40Hz to 120Hz · Default: 55Hz"}, "beat.bd_decay": {"bd_decay · track override (ms)", `name="value" value="400"`, "80ms to 1.5s · Default: 400ms"}} {
		row := parameter(body, path)
		for _, text := range texts {
			if !strings.Contains(row, text) {
				t.Errorf("%s missing %q: %s", path, text, row)
			}
		}
	}
}

// mixerShellApp serves the Studio shell over the mixer fixture: the tracks keys
// (acid) and bass (audio), the music bus, the master, two returns and a chorus
// on no insert chain. It records the edits the mixer action sends.
func mixerShellApp(t *testing.T, edition int) (string, *http.Client, *[]map[string]any) {
	t.Helper()
	source := "cicada 2\ntrack keys acid {}\ntrack bass audio {}\npattern p notes { c4 . }\nscene main { keys=p }\nsong { main*4 }\n"
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	state := mixerFixture(edition)
	var mu sync.Mutex
	var edits []map[string]any
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Source: source, Filename: "test.cicada", Revision: state.Revision, Valid: true, Project: p})
		case r.URL.Path == "/api/mixer" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(state)
		case r.URL.Path == "/api/mixer":
			var edit map[string]any
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil {
				t.Error(err)
			}
			if edit["revision"] != state.Revision {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"error":"score changed"}`)
				return
			}
			edits = append(edits, edit)
			_, _ = io.WriteString(w, `{"revision":"r2","valid":true}`)
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

// deviceRegion cuts the Device dock's region out of a shell page.
func deviceRegion(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `id="update-dock-device"`)
	end := strings.Index(body, `id="update-dock-automation"`)
	if start < 0 || end < start {
		t.Fatal("page lacks the Device region")
	}
	return body[start:end]
}

func TestMixerStripOpensBusMasterReturnAndEffectInDevice(t *testing.T) {
	address, client, edits := mixerShellApp(t, 2)
	open := func(ref string) (string, string) {
		body := getPage(t, client, address+"/studio?tab=mixer&dock=device&deviceTrack="+url.QueryEscape(ref))
		return body, deviceRegion(t, body)
	}
	has := func(name, device string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(device, want) {
				t.Errorf("%s in Device lacks %s", name, want)
			}
		}
	}

	// A bus: exact level with its saved choices, mute and solo, and the
	// controls the service does not support, each named with a plain reason.
	body, device := open("bus:music")
	has("the music bus", device, `data-device-channel="bus:music"`, "<h2>music · Bus</h2>", `aria-label="Mix and routing"`, `aria-label="Insert effects"`,
		// The bus level takes the values the score writes, so it posts them as text.
		`<input type="hidden" name="path" value="music.level" /><input type="hidden" name="valueType" value="text" />`, `<select name="value"><option value="-3dB" selected>-3dB</option><option value="off">off</option></select>`,
		`name="path" value="music.mute"`, `name="path" value="music.solo"`,
		`data-reason="CICADA-UNSUPPORTED: bus music pan is not implemented"`, "<span>pan</span> <strong>Not available</strong>", "Bus music pan is not implemented.",
		`data-reason="CICADA-UNSUPPORTED: bus music output routing is not implemented"`, "<span>out</span> <strong>Not available</strong>", "Bus music output routing is not implemented.",
		`name="__gosx_return_to" value="/studio?deviceTrack=bus%3Amusic&amp;dock=device&amp;tab=mixer"`,
		`<optgroup label="Tracks"><option value="keys">keys</option><option value="bass">bass</option></optgroup>`,
		`<optgroup label="Buses and master"><option value="bus:music" selected>music</option><option value="master:master">Master</option></optgroup>`,
		`<optgroup label="Returns"><option value="return:echo">echo</option><option value="return:room">room</option></optgroup>`,
		`<optgroup label="Effects on no channel"><option value="effect:wide">wide</option></optgroup>`, "Inspect channel")
	if strings.Contains(device, `aria-label="Sound"`) || strings.Contains(device, "data-device-track") {
		t.Error("a bus has no instrument controls")
	}
	// The diagnostic code stays in data-reason; the sentence a person reads has none.
	if visible := regexp.MustCompile(`data-reason="[^"]*"`).ReplaceAllString(device, ""); strings.Contains(visible, "CICADA-UNSUPPORTED") {
		t.Error("Device prints the raw refusal code")
	}
	// The dock header names the bus and the Mixer marks its strip.
	if !strings.Contains(body, `data-dock-selection="track"><span>music · Bus</span>`) {
		t.Error("the dock header should name the bus")
	}
	if strip := mixerStripHTML(t, body, "bus:music"); !strings.Contains(strip, `data-mixer-selected="true"`) || strings.Count(body, `data-mixer-selected="true"`) != 1 {
		t.Error("only the bus strip is marked selected")
	}

	// A Device form for the bus saves one value and returns to the bus in Device.
	returnTo := "/studio?deviceTrack=bus%3Amusic&dock=device&tab=mixer"
	form := url.Values{"csrf_token": {tokenFromPage(t, body)}, "revision": {"r1"}, "path": {"music.level"}, "value": {"off"}, "valueType": {"text"}, "__gosx_return_to": {returnTo}}
	response := shellPost(t, client, address, "/__actions/mixer", form, address+"/studio?tab=mixer&dock=device&deviceTrack=bus%3Amusic")
	if response.StatusCode != 303 || response.Header.Get("Location") != returnTo || len(*edits) != 1 || (*edits)[0]["path"] != "music.level" || (*edits)[0]["value"] != "off" {
		t.Fatalf("bus level: status=%d return=%s edits=%+v", response.StatusCode, response.Header.Get("Location"), *edits)
	}
	// A strip's own save (here M on keys) also keeps the bus in Device: the
	// form names the mixer panel, and the shell adds the channel from the page.
	face := "/studio?deviceTrack=bus%3Amusic&dock=device&tab=mixer"
	if !strings.Contains(mixerStripHTML(t, body, "keys"), `name="__gosx_return_to" value="/?panel=mixer"`) {
		t.Error("strip forms should name the mixer panel destination")
	}
	form = url.Values{"csrf_token": {tokenFromPage(t, body)}, "revision": {"r1"}, "path": {"keys.mute"}, "value": {"false"}, "valueType": {"bool"}, "__gosx_return_to": {"/?panel=mixer"}}
	response = shellPost(t, client, address, "/__actions/mixer", form, address+face+"&pattern=p")
	if response.StatusCode != 303 || response.Header.Get("Location") != face || len(*edits) != 2 || (*edits)[1]["path"] != "keys.mute" {
		t.Fatalf("strip mute: status=%d return=%s edits=%+v", response.StatusCode, response.Header.Get("Location"), *edits)
	}

	// The master: exact level, which also takes "off".
	body, device = open("master:master")
	has("the master", device, `data-device-channel="master:master"`, "<h2>Master</h2>", `name="path" value="master.level"`, "level (dB) · off or -60 to 6", `name="valueType" value="text"`,
		`name="path" value="master.mute"`, `<option value="master:master" selected>Master</option>`)
	if !strings.Contains(body, `data-dock-selection="track"><span>Master</span>`) {
		t.Error("the dock header should name the master")
	}

	// A return: the effect's parameters, each once.
	_, device = open("return:echo")
	has("the echo return", device, `data-device-channel="return:echo"`, "<h2>echo · Return</h2>", `id="device-effect-echo"`, `aria-label="Return effect"`, `<option value="return:echo" selected>echo</option>`)
	for _, path := range []string{"echo.time", "echo.feedback"} {
		if got := strings.Count(device, `name="path" value="`+path+`"`); got != 1 {
			t.Errorf("%s has %d forms in Device", path, got)
		}
	}

	// An effect on no insert chain: its parameters and where to put it.
	_, device = open("effect:wide")
	has("the loose chorus", device, `data-device-channel="effect:wide"`, "<h2>wide · chorus</h2>", `id="device-effect-wide"`, `name="path" value="wide.mix"`, "This effect is on no channel.", `<option value="effect:wide" selected>wide</option>`)

	for _, ref := range []string{"master", "master:master"} {
		_, device = open(ref)
		has(ref, device, `data-device-channel="master:master"`)
	}
	for _, ref := range []string{"bus:nowhere", "return:gone", "track:gone", "effect:gone"} {
		_, device = open(ref)
		if !strings.Contains(device, "This channel no longer exists. Select a strip in the Mixer.") || strings.Contains(device, `name="path"`) {
			t.Errorf("%s should show the empty state without forms", ref)
		}
	}
	if _, device = open("missing"); !strings.Contains(device, `data-device-track="keys"`) {
		t.Error("an unknown plain track ID should fall back to the first track")
	}
}

func TestDeviceHoldsEveryControlThatLeftTheStripFace(t *testing.T) {
	address, client, _ := mixerShellApp(t, 2)
	device := deviceRegion(t, getPage(t, client, address+"/studio?tab=mixer&dock=device&deviceTrack=keys"))
	for _, want := range []string{`data-device-track="keys"`, "<h2>keys · acid</h2>", `aria-label="Sound"`,
		// Exact level (a text field, so it takes "off"), exact pan, routing and the send level.
		`name="path" value="keys.level"`, "level (dB) · off or -60 to 6", `name="path" value="keys.pan"`, `type="number"`,
		`name="path" value="keys.out"`, `<option value="music" selected>music</option>`, `<option value="sfx">sfx</option>`,
		`name="path" value="keys.send.echo"`, "send to echo (ratio)", `name="path" value="keys.mute"`,
		// The insert chain editor and each insert's parameters.
		`aria-label="keys insert chain"`, `aria-label="Move squash earlier"`, `aria-label="Remove tone"`, `href="#device-effect-tone"`,
		`id="device-effect-tone"`, `name="path" value="tone.hp"`, "tone.hp (Hz)", `id="device-effect-squash"`, `name="path" value="squash.ratio"`,
	} {
		if !strings.Contains(device, want) {
			t.Errorf("Device for keys lacks %s", want)
		}
	}
	if strings.Contains(device, "Add vocal chain") || strings.Contains(device, "Upgrade score to edition 2") {
		t.Error("keys is not an audio track, and edition 2 needs no upgrade")
	}
	if !strings.Contains(device, `class="device-groups" data-gosx-key="device-keys"`) {
		t.Error("keys Device groups lack their channel key")
	}
	// An audio track keeps the vocal chain action.
	device = deviceRegion(t, getPage(t, client, address+"/studio?tab=mixer&dock=device&deviceTrack=bass"))
	if !strings.Contains(device, `class="device-groups" data-gosx-key="device-bass"`) {
		t.Error("bass Device groups lack their distinct channel key")
	}
	if !regexp.MustCompile(`(?s)<form[^>]*>(?:<input[^>]*>)*<input type="hidden" name="action" value="vocal-chain" /><input type="hidden" name="track" value="bass" /><button[^>]*>Add vocal chain</button></form>`).MatchString(device) {
		t.Errorf("Device for an audio track lacks the vocal chain action")
	}
}

// An edition 1 score has no faders or one-click buttons, so its exact-value
// forms in Device, each with the upgrade confirmation, are the way to mix.
func TestDeviceKeepsEdition1SetFormsWithTheUpgradeConfirmation(t *testing.T) {
	address, client, edits := mixerShellApp(t, 1)
	body := getPage(t, client, address+"/studio?tab=mixer&dock=device&deviceTrack=bus%3Amusic")
	mixer := body[strings.Index(body, `id="update-center-mixer"`):strings.Index(body, `id="update-dock-header"`)]
	if strings.Contains(mixer, `type="range"`) || strings.Contains(mixer, `name="path"`) {
		t.Error("an edition 1 strip face has no direct controls")
	}
	for _, channel := range []string{"keys", "bus:music", "master:master", "return:echo"} {
		device := deviceRegion(t, getPage(t, client, address+"/studio?tab=mixer&dock=device&deviceTrack="+url.QueryEscape(channel)))
		if channel == "keys" {
			for _, path := range []string{"keys.level", "keys.pan", "keys.mute"} {
				if !strings.Contains(device, `name="path" value="`+path+`"`) {
					t.Errorf("edition 1 Device lacks the %s form", path)
				}
			}
		}
		forms := regexp.MustCompile(`(?s)<form[^>]*action="/__actions/mixer".*?</form>`).FindAllString(device, -1)
		if len(forms) == 0 {
			t.Fatalf("%s has no mixer forms in Device", channel)
		}
		for _, form := range forms {
			if strings.Contains(form, `name="path"`) && !strings.Contains(form, `name="confirmUpgrade" required`) {
				t.Errorf("%s: an edition 1 Set form lacks the upgrade confirmation: %s", channel, form)
			}
		}
	}
	returnTo := "/studio?deviceTrack=bus%3Amusic&dock=device&tab=mixer"
	form := url.Values{"csrf_token": {tokenFromPage(t, body)}, "revision": {"r1"}, "path": {"music.mute"}, "value": {"true"}, "valueType": {"bool"}, "confirmUpgrade": {"on"}, "__gosx_return_to": {returnTo}}
	response := shellPost(t, client, address, "/__actions/mixer", form, address+returnTo)
	if response.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["path"] != "music.mute" || (*edits)[0]["confirmUpgrade"] != true {
		t.Fatalf("edition 1 mute: status=%d edits=%+v", response.StatusCode, *edits)
	}
}
