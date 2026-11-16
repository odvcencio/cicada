package main

import (
	"context"
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

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	gosxserver "m31labs.dev/gosx/server"
)

func toggleField(path string, on any) mixerField {
	return mixerField{Path: path, Value: on, Supported: true, Descriptor: paramdefs.Descriptor{Curve: "toggle"}}
}

func TestMixerToggleSavesTheOppositeValue(t *testing.T) {
	s := &studioApp{}
	html := gosx.RenderHTML(s.mixerToggle(workspace{Edition: 2}, "token", "mixer", "keys", "mute", toggleField("keys.mute", false)))
	for _, want := range []string{`class="strip-toggle"`, `aria-pressed="false"`, `aria-label="Mute keys"`, `name="path" value="keys.mute"`, `name="valueType" value="bool"`, `name="value" value="true"`, `action="/__actions/mixer"`} {
		if !strings.Contains(html, want) {
			t.Errorf("mute button missing %s in %s", want, html)
		}
	}
	// A saved mute shows pressed and posts false. Default registry values are
	// numeric, so 1 also means on.
	for _, on := range []any{true, float64(1)} {
		html = gosx.RenderHTML(s.mixerToggle(workspace{Edition: 2}, "token", "mixer", "keys", "solo", toggleField("keys.solo", on)))
		if !strings.Contains(html, `aria-pressed="true"`) || !strings.Contains(html, `name="value" value="false"`) || !strings.Contains(html, `name="path" value="keys.solo"`) {
			t.Fatalf("saved solo %v: %s", on, html)
		}
	}
}

func TestMixerControlsLeaveEdition1AndUnsupportedFieldsToTheSetForms(t *testing.T) {
	s := &studioApp{}
	pan := mixerField{Path: "keys.pan", Value: 0.0, Supported: true, Descriptor: paramdefs.Descriptor{Min: -1, Max: 1}}
	if html := gosx.RenderHTML(s.mixerToggle(workspace{Edition: 1}, "token", "mixer", "keys", "mute", toggleField("keys.mute", false))); html != "" {
		t.Fatalf("edition 1 mute button: %s", html)
	}
	if html := gosx.RenderHTML(s.mixerPan(workspace{Edition: 1}, "token", "keys", pan)); html != "" {
		t.Fatalf("edition 1 pan control: %s", html)
	}
	unsupported := mixerField{Path: "music.pan", Supported: false, Reason: "CICADA-UNSUPPORTED"}
	if html := gosx.RenderHTML(s.mixerPan(workspace{Edition: 2}, "token", "music", unsupported)) + gosx.RenderHTML(s.mixerToggle(workspace{Edition: 2}, "token", "mixer", "music", "mute", unsupported)); html != "" {
		t.Fatalf("unsupported field: %s", html)
	}
}

func TestMixerPanSavesOncePerGestureAndResetsToCentre(t *testing.T) {
	s := &studioApp{}
	pan := mixerField{Path: "keys.pan", Value: -0.35, Supported: true, Descriptor: paramdefs.Descriptor{Min: -1, Max: 1}}
	html := gosx.RenderHTML(s.mixerPan(workspace{Edition: 2}, "token", "keys", pan))
	for _, want := range []string{`type="range"`, `min="-1"`, `max="1"`, `step="0.01"`, `value="-0.35"`, `aria-label="keys pan"`, `data-commit="settle"`, `aria-valuetext="35% left"`, `name="path" value="keys.pan"`, `name="valueType" value="number"`, `>L35<`, `aria-label="Centre keys pan"`, `name="value" value="0"`} {
		if !strings.Contains(html, want) {
			t.Errorf("pan missing %s in %s", want, html)
		}
	}
	pan.Value = 0.0
	html = gosx.RenderHTML(s.mixerPan(workspace{Edition: 2}, "token", "keys", pan))
	if !strings.Contains(html, `>C<`) || !strings.Contains(html, "disabled") {
		t.Fatalf("centred pan: %s", html)
	}
	if got := panText(0.5); got != "R50" {
		t.Fatalf("panText(0.5) = %q", got)
	}
	// The engine saves a slider once per gesture, so no range input may submit on every change.
	for _, html := range []string{html, gosx.RenderHTML(s.mixerFader(workspace{Edition: 2}, "token", "keys", mixerField{Path: "keys.level", Value: -6.0, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6}}))} {
		if strings.Contains(html, "data-gosx-submit-on") || !strings.Contains(html, `data-commit="settle"`) {
			t.Fatalf("range inputs commit through the engine: %s", html)
		}
	}
	for value, want := range map[float64]string{0: "centre", -0.35: "35% left", 0.35: "35% right"} {
		if got := panSpoken(value); got != want {
			t.Errorf("panSpoken(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestSendTextShowsTheLevelAsTheScoreWritesIt(t *testing.T) {
	for want, send := range map[string]mixerSend{
		"echo -12 dB":   {To: "echo", Level: "-12dB", mixerField: mixerField{Value: 0.251}},
		"room 0.25 pre": {To: "room", Level: "0.25", Pre: true, mixerField: mixerField{Value: 0.25}},
		"hall 0.33":     {To: "hall", mixerField: mixerField{Value: 0.3333}},
		"tape -6 dB":    {To: "tape", mixerField: mixerField{Value: -6.0, Descriptor: paramdefs.Descriptor{Unit: "dB"}}},
		"plate off":     {To: "plate", mixerField: mixerField{Value: "off"}},
		"spring":        {To: "spring"},
	} {
		if got := sendText(send); got != want {
			t.Errorf("sendText = %q, want %q", got, want)
		}
	}
}

func TestStripMeterCarriesItsIDAndAnAccessibleLabel(t *testing.T) {
	html := gosx.RenderHTML(stripMeter("track:keys", "keys"))
	for _, want := range []string{`data-meter="track:keys"`, `data-meter-name="keys"`, `data-clip="false"`, `role="img"`, `aria-live="off"`, `aria-label="keys meter, no signal"`, `strip-meter__peak`, `strip-meter__rms`, `strip-meter__hold`, `strip-meter__clip`} {
		if !strings.Contains(html, want) {
			t.Errorf("meter missing %s in %s", want, html)
		}
	}
	loudness := gosx.RenderHTML(masterLoudness())
	for _, key := range []string{"momentary", "short_term", "integrated", "true_peak"} {
		if !strings.Contains(loudness, `data-loudness="`+key+`"`) {
			t.Errorf("master loudness missing %s", key)
		}
	}
}

// mixerFixture is a saved mixer state shaped like the service's: two tracks
// (keys carries two inserts and a send), the music bus with an unsupported pan,
// the master, a delay return, a metered reverb return, and a chorus on no
// insert chain.
func mixerFixture(edition int) mixerState {
	strip := func(id string, muted bool) mixerStrip {
		return mixerStrip{ID: id, Name: id, Fields: map[string]mixerField{
			"level": {Path: id + ".level", Value: -6.0, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6, Unit: "dB", Off: true}},
			"pan":   {Path: id + ".pan", Value: 0.25, Supported: true, Descriptor: paramdefs.Descriptor{Min: -1, Max: 1}},
			"mute":  toggleField(id+".mute", muted),
			"solo":  toggleField(id+".solo", false),
		}}
	}
	keys := strip("keys", true)
	keys.SourceKind = "acid"
	keys.Inserts = []string{"tone", "squash"}
	keys.Fields["insert"] = mixerField{Path: "keys.insert", Value: "tone -> squash", Supported: true}
	keys.Fields["out"] = mixerField{Path: "keys.out", Value: "music", Supported: true, Descriptor: paramdefs.Descriptor{Curve: "enum", Values: []string{"music", "sfx"}}}
	keys.Sends = []mixerSend{{To: "echo", Level: "-12dB", mixerField: mixerField{Path: "keys.send.echo", Value: 0.25, Supported: true, Descriptor: paramdefs.Descriptor{Min: 0, Max: 1, Unit: "ratio"}}}}
	bass := strip("bass", false)
	bass.SourceKind = "audio"
	bass.Fields["insert"] = mixerField{Path: "bass.insert", Value: "", Supported: true}
	music := strip("music", false)
	music.Fields["level"] = mixerField{Path: "music.level", Value: -3.0, Choices: []string{"-3dB", "off"}, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6, Unit: "dB"}}
	music.Fields["pan"] = mixerField{Path: "music.pan", Reason: "CICADA-UNSUPPORTED: bus music pan is not implemented"}
	music.Fields["out"] = mixerField{Path: "music.out", Reason: "CICADA-UNSUPPORTED: bus music output routing is not implemented"}
	master := strip("master", false)
	master.Name = "Master"
	delete(master.Fields, "pan")
	echo := mixerReturn{Name: "echo", Kind: "delay", Meter: "a", Fields: []mixerField{{Path: "echo.time", Value: 0.25, Supported: true}, {Path: "echo.feedback", Value: 0.3, Supported: true}}}
	return mixerState{Edition: edition, Revision: "r1", Tracks: []mixerStrip{keys, bass}, Buses: []mixerStrip{music}, Master: master, Returns: []mixerReturn{echo},
		Effects: []mixerReturn{echo, {Name: "room", Kind: "reverb", Meter: "b"},
			{Name: "tone", Kind: "eq", Fields: []mixerField{{Path: "tone.hp", Value: 120.0, Supported: true, Descriptor: paramdefs.Descriptor{Unit: "Hz", Min: 20, Max: 20000}}}},
			{Name: "squash", Kind: "comp", Fields: []mixerField{{Path: "squash.ratio", Value: 4.0, Supported: true}}},
			{Name: "wide", Kind: "chorus", Fields: []mixerField{{Path: "wide.mix", Value: 0.5, Supported: true}}}}}
}

// mixerHTML renders the Mixer for a request to target against a backend whose
// /api/mixer answers with state.
func mixerHTML(t *testing.T, state mixerState, target string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(state) }))
	t.Cleanup(server.Close)
	backend, err := newBackend(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &studioApp{backend: backend}
	return gosx.RenderHTML(app.mixer(&gosxserver.Context{Request: httptest.NewRequest(http.MethodGet, target, nil)}, workspace{}, "token"))
}

// mixerPageHTML renders the Mixer view of the Studio shell for the fixture.
func mixerPageHTML(t *testing.T, edition int) string {
	t.Helper()
	return mixerHTML(t, mixerFixture(edition), "/studio?tab=mixer")
}

// mixerStripHTML cuts one strip out of the Mixer markup by its channel.
func mixerStripHTML(t *testing.T, html, channel string) string {
	t.Helper()
	start := strings.Index(html, `data-mixer-channel="`+channel+`"`)
	if start < 0 {
		t.Fatalf("no strip for %s", channel)
	}
	start = strings.LastIndex(html[:start], "<section")
	end := strings.Index(html[start:], "</section>")
	return html[start : start+end]
}

func TestMixerStripsCarryMeterIDsAndToggles(t *testing.T) {
	html := mixerPageHTML(t, 2)
	for _, want := range []string{`data-meter="track:keys"`, `data-meter="track:bass"`, `data-meter="bus:music"`, `data-meter="master"`, `data-meter="return:b"`, `data-loudness="integrated"`,
		`aria-label="Mute keys"`, `data-toggle="mute"`, `name="path" value="keys.mute"`, `name="path" value="keys.pan"`, `data-mixer-pan="keys.pan"`} {
		if !strings.Contains(html, want) {
			t.Errorf("Mixer missing %s", want)
		}
	}
	// keys is saved as muted, so its button is pressed and posts false; bass is not.
	if strings.Count(html, `aria-pressed="true"`) != 1 || !strings.Contains(html, `name="path" value="keys.mute"`) {
		t.Errorf("only the saved mute should be pressed:\n%s", html)
	}
	if strings.Count(html, `data-meter="master"`) != 1 || strings.Count(html, `data-loudness="momentary"`) != 1 {
		t.Error("only the master strip shows loudness")
	}
}

func TestMixerEdition1HasMetersButNoOneClickControls(t *testing.T) {
	html := mixerPageHTML(t, 1)
	if !strings.Contains(html, `data-meter="track:keys"`) {
		t.Error("edition 1 still shows meters")
	}
	for _, banned := range []string{`class="strip-toggle"`, `data-mixer-pan`, `type="range"`} {
		if strings.Contains(html, banned) {
			t.Errorf("edition 1 must keep only the confirmed Set forms, found %s", banned)
		}
	}
	if !strings.Contains(html, "Upgrade score to edition 2") {
		t.Error("the edition 1 chorus form keeps the upgrade confirmation")
	}
	// Without a fader or a pan slider the strip still shows the saved values.
	keys := mixerStripHTML(t, html, "keys")
	for _, want := range []string{`<span class="strip-readout">-6.0<small>dB</small></span>`, `<span class="strip-pan-readout">R25</span>`} {
		if !strings.Contains(keys, want) {
			t.Errorf("edition 1 strip missing %s in %s", want, keys)
		}
	}
}

func TestArrangeRailShowsMuteAndSoloPerTrack(t *testing.T) {
	for _, edition := range []int{1, 2} {
		state := mixerState{Edition: edition, Revision: "r9", Tracks: []mixerStrip{{ID: "voice", Name: "voice", Fields: map[string]mixerField{"mute": toggleField("voice.mute", true), "solo": toggleField("voice.solo", false)}}}}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(state) }))
		t.Cleanup(server.Close)
		backend, err := newBackend(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		app := &studioApp{backend: backend}
		rail := app.trackRail(&gosxserver.Context{Request: httptest.NewRequest(http.MethodGet, "/studio", nil)}, workspace{}, "token")
		p := &project.Project{Tracks: []project.Track{{ID: "voice", Kind: "audio"}}, Arrange: &project.Arrangement{}}
		html := gosx.RenderHTML(placementTimeline(p, nil, "", 3840, rail))
		if edition == 1 {
			if strings.Contains(html, "strip-toggle") {
				t.Errorf("edition 1 rail has one-click buttons: %s", html)
			}
			continue
		}
		for _, want := range []string{`class="rail-toggles"`, `name="path" value="voice.mute"`, `name="path" value="voice.solo"`, `aria-pressed="true"`, `aria-pressed="false"`, `name="revision" value="r9"`, `value="/?panel=session"`} {
			if !strings.Contains(html, want) {
				t.Errorf("rail missing %s", want)
			}
		}
		if strings.Contains(gosx.RenderHTML(rail("nobody")), "strip-toggle") {
			t.Error("unknown track gets no buttons")
		}
	}
}

func TestMixerRendersOneStripPerReturnAndListsLooseEffects(t *testing.T) {
	delay := mixerReturn{Name: "echo", Meter: "a", Fields: []mixerField{{Path: "echo.time", Value: 0.25, Supported: true}, {Path: "echo.mix", Value: 0.5, Supported: true}}}
	// The server lists delay and reverb as effects and again as returns.
	state := mixerState{Edition: 2, Revision: "r1", Master: mixerStrip{ID: "master", Name: "Master"},
		Returns: []mixerReturn{delay, {Name: "hall", Meter: "b", Fields: []mixerField{{Path: "hall.size", Value: 0.5, Supported: true}}}},
		Effects: []mixerReturn{{Name: "echo", Fields: []mixerField{{Path: "echo.time", Value: 0.25, Supported: true}, {Path: "echo.feedback", Value: 0.3, Supported: true}}}, {Name: "hall", Meter: "b", Fields: []mixerField{{Path: "hall.size", Value: 0.5, Supported: true}}}, {Name: "tone"}}}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _ = json.NewEncoder(w).Encode(state) }))
	t.Cleanup(server.Close)
	backend, err := newBackend(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &studioApp{backend: backend}
	html := gosx.RenderHTML(app.mixer(&gosxserver.Context{Request: httptest.NewRequest(http.MethodGet, "/studio?tab=mixer", nil)}, workspace{}, "token"))
	for _, once := range []string{`data-mixer-channel="return:echo"`, `data-mixer-channel="return:hall"`, `data-meter="return:a"`, `data-meter="return:b"`, `data-mixer-select="effect:tone"`} {
		if strings.Count(html, once) != 1 {
			t.Errorf("%s appears %d times", once, strings.Count(html, once))
		}
	}
	// tone is on no insert chain: it has no strip, and the tool strip opens it in Device.
	if strings.Contains(html, `data-mixer-channel="effect:tone"`) || !strings.Contains(html, `deviceTrack=effect%3Atone`) {
		t.Errorf("an effect on no channel is listed, not shown as a strip:\n%s", html)
	}
	// A return's parameters are in Device, not on its strip.
	for _, path := range []string{"echo.time", "echo.mix", "echo.feedback", "hall.size"} {
		if strings.Contains(html, `name="path" value="`+path+`"`) {
			t.Errorf("field %s is on the strip face", path)
		}
	}
	// Device merges both entries of a return, each path once.
	strips, _ := mixerChannels(state, nil)
	for _, c := range strips {
		if c.ID != "echo" {
			continue
		}
		var paths []string
		for _, f := range c.Effect.Fields {
			paths = append(paths, f.Path)
		}
		if got := strings.Join(paths, " "); got != "echo.time echo.mix echo.feedback" {
			t.Errorf("merged echo fields = %q", got)
		}
	}
	// The master is the last strip, after the returns.
	if last := strips[len(strips)-1]; last.Kind != "master" || strips[len(strips)-2].Kind != "return" {
		t.Errorf("strip order ends %s, %s", strips[len(strips)-2].Kind, last.Kind)
	}
	if calls != 1 {
		t.Errorf("mixer read %d times", calls)
	}
}

func TestMixerStripFaceHasNoSetFormsAndNoRawRefusals(t *testing.T) {
	html := mixerPageHTML(t, 2)
	// The exact-value forms and the routing, insert and effect editors are in Device.
	for _, banned := range []string{`class="mixer-control`, `class="effect-chain"`, ">Set<", "Add vocal chain", `name="path" value="keys.out"`, `name="path" value="keys.send.echo"`, `name="path" value="tone.hp"`, `name="path" value="echo.time"`} {
		if strings.Contains(html, banned) {
			t.Errorf("the strip face still holds %s", banned)
		}
	}
	// The music bus has an unsupported pan and output; neither refusal is printed.
	for _, banned := range []string{"CICADA-UNSUPPORTED", "is not implemented"} {
		if strings.Contains(html, banned) {
			t.Errorf("the Mixer view prints %q", banned)
		}
	}
	if music := mixerStripHTML(t, html, "bus:music"); !strings.Contains(music, `<span class="strip-none">No pan</span>`) || strings.Contains(music, "data-mixer-pan") {
		t.Errorf("a bus without pan says so plainly: %s", music)
	}
	// One form per direct control: the M and S buttons, the fader, and the pan
	// slider with its centre button.
	for path, want := range map[string]int{"keys.mute": 1, "keys.solo": 1, "keys.level": 1, "keys.pan": 2, "music.mute": 1, "music.level": 0, "master.level": 1} {
		if got := strings.Count(html, `name="path" value="`+path+`"`); got != want {
			t.Errorf("%s has %d forms on the face, want %d", path, got, want)
		}
	}
	// The service takes only "-3dB" or "off" for the music bus level and refuses
	// any other value, so its fader shows the level and cannot move.
	if music := mixerStripHTML(t, html, "bus:music"); !strings.Contains(music, `value="-3" aria-label="music level is set in Device: -3dB or off" title="music level is set in Device: -3dB or off" disabled`) || strings.Contains(music, "data-commit") || !strings.Contains(music, `-3.0<small>dB</small>`) {
		t.Errorf("a bus level that takes only listed values has a fixed fader: %s", music)
	}
	// Edition 1 confirms an upgrade with each change, so its face has no mixer forms at all.
	edition1 := mixerPageHTML(t, 1)
	if strings.Contains(edition1, `name="path"`) || strings.Contains(edition1, "CICADA-UNSUPPORTED") {
		t.Errorf("edition 1 face holds a mixer form or a refusal")
	}
}

func TestMixerStripsShareTheirRowsAndOpenInDevice(t *testing.T) {
	html := mixerHTML(t, mixerFixture(2), "/studio?tab=mixer&deviceTrack=bus:music&pattern=p&step=3&file=b.cicada")
	rows := []string{`class="strip-name"`, `class="strip-kind"`, `class="strip-inserts`, `class="strip-sends"`, `class="strip-toggles"`, `class="strip-pan"`, `class="strip-channel"`}
	labels := map[string]string{"keys": "acid", "bass": "audio", "bus:music": "Bus", "return:echo": "Return", "return:room": "Return", "master:master": "Master"}
	if got := strings.Count(html, `<section class="strip"`); got != len(labels) {
		t.Fatalf("%d strips, want %d", got, len(labels))
	}
	for channel, label := range labels {
		strip := mixerStripHTML(t, html, channel)
		at := 0
		for _, row := range rows {
			next := strings.Index(strip[at:], row)
			if next < 0 || strings.Count(strip, row) != 1 {
				t.Errorf("%s: row %s is missing, repeated or out of order", channel, row)
				break
			}
			at += next
		}
		if !strings.Contains(strip, `<p class="strip-kind">`+label+`</p>`) {
			t.Errorf("%s should say it is %q: %s", channel, label, strip)
		}
	}
	keys := mixerStripHTML(t, html, "keys")
	for _, want := range []string{`<ol class="strip-inserts" aria-label="keys inserts"><li>tone</li><li>squash</li></ol>`, `<ul class="strip-sends" aria-label="keys sends"><li>echo -12 dB</li></ul>`,
		`<a href="/studio?deviceTrack=keys&amp;dock=device&amp;file=b.cicada&amp;tab=mixer" data-mixer-select="keys" aria-label="Open keys in Device">keys</a>`, `data-track-color="0"`} {
		if !strings.Contains(keys, want) {
			t.Errorf("keys strip missing %s in %s", want, keys)
		}
	}
	if bass := mixerStripHTML(t, html, "bass"); !strings.Contains(bass, `<p class="strip-inserts strip-none">No inserts</p>`) || !strings.Contains(bass, `<div class="strip-sends"></div>`) {
		t.Errorf("a strip without inserts or sends keeps the rows: %s", bass)
	}
	// The selected strip is the channel Device shows, and only that one.
	music := mixerStripHTML(t, html, "bus:music")
	for _, want := range []string{`data-mixer-selected="true"`, `href="/studio?deviceTrack=bus%3Amusic&amp;dock=device&amp;file=b.cicada&amp;tab=mixer"`, `aria-label="Open music in Device" aria-current="true"`} {
		if !strings.Contains(music, want) {
			t.Errorf("selected bus strip missing %s in %s", want, music)
		}
	}
	if strings.Count(html, `data-mixer-selected="true"`) != 1 || strings.Count(html, `aria-current="true"`) != 1 {
		t.Error("exactly one strip is selected")
	}
	// The Mixer's markup names no selection of another view (here the pattern
	// and step), so an edit there leaves this region as it is. Its forms keep
	// the constant panel destination, which the shell resolves when they post.
	if strings.Contains(html, "pattern=p") || strings.Contains(html, "step=3") {
		t.Error("the Mixer markup should not carry another view's selection")
	}
	if got := strings.Count(html, `name="__gosx_return_to" value="/?panel=mixer"`); got != strings.Count(html, `name="__gosx_return_to"`) || got == 0 {
		t.Errorf("every Mixer form returns to the mixer panel destination, %d did", got)
	}
	// A save from the Mixer comes back with the channel Device shows; a save
	// from another panel does not carry it.
	from, _ := url.Parse("/studio?tab=mixer&dock=device&deviceTrack=bus:music&pattern=p")
	if got := shellDestination(from, "/?panel=mixer", true); got != "/studio?deviceTrack=bus%3Amusic&dock=device&tab=mixer" {
		t.Errorf("mixer destination = %s", got)
	}
	placed, _ := url.Parse("/studio?tab=mixer&placement=one")
	if got := shellDestination(placed, "/?panel=mixer", true); got != "/studio?placement=one&tab=mixer" {
		t.Errorf("mixer destination with placement = %s", got)
	}
	if got := shellDestination(from, "/?panel=session&placement=one", true); got != "/studio?dock=device&placement=one&tab=mixer" {
		t.Errorf("session destination = %s", got)
	}
	// Without an explicit choice Device shows the first track, so that strip is marked.
	if first := mixerStripHTML(t, mixerPageHTML(t, 2), "keys"); !strings.Contains(first, `data-mixer-selected="true"`) {
		t.Error("the first track is selected by default")
	}
	if master := mixerStripHTML(t, html, "master:master"); !strings.Contains(master, `deviceTrack=master`) || !strings.Contains(master, `data-mixer-select="master"`) {
		t.Error("the master strip should use the short address")
	}
	// A master strip keeps its loudness readout, below the shared rows.
	if master := mixerStripHTML(t, html, "master:master"); strings.Index(master, `class="strip-loudness"`) < strings.Index(master, `class="strip-channel"`) {
		t.Error("the master loudness follows the shared rows")
	}
}

func TestMixerToolStripFiltersInTheTabAndKeepsTheChorusForm(t *testing.T) {
	html := mixerPageHTML(t, 2)
	tools := html[strings.Index(html, `class="mixer-tools"`):strings.Index(html, `<div class="strips">`)]
	for _, want := range []string{
		// Each view select has an id: GoSX keeps a select across a page update,
		// and with it the choice, only when it has an id or a name.
		`<select id="mixer-show" data-mixer-show="" data-studio-view-control><option value="all">All channels</option><option value="tracks">Tracks</option><option value="buses">Buses and returns</option></select>`,
		`<select id="mixer-meter" data-mixer-meter="" data-studio-view-control><option value="both">Peak and RMS</option><option value="peak">Peak</option><option value="rms">RMS</option></select>`,
		`<button type="button" data-mixer-reset-peaks=""`, ">Reset peaks</button>",
		`<details class="mixer-add-effect"><summary>Add effect</summary>`, `name="action" value="add-chorus"`, `<span>Chorus name</span><input name="name" value="chorus"`, ">Create chorus</button>",
		// wide is on no insert chain; tone and squash are on the keys chain.
		`data-mixer-select="effect:wide"`, `aria-label="Open wide in Device"`,
	} {
		if !strings.Contains(tools, want) {
			t.Errorf("tool strip missing %s in %s", want, tools)
		}
	}
	for _, banned := range []string{`data-mixer-select="effect:tone"`, `data-mixer-select="effect:squash"`, "confirmUpgrade"} {
		if strings.Contains(tools, banned) {
			t.Errorf("tool strip should not hold %s", banned)
		}
	}
	// The view controls are outside both forms, so they post nothing.
	forms := regexp.MustCompile(`(?s)<form.*?</form>`).FindAllString(tools, -1)
	if len(forms) != 2 {
		t.Fatalf("the tool strip holds chorus and return forms: got %d", len(forms))
	}
	for i, wants := range [][]string{
		{`action="/__actions/mixer"`, `name="action" value="add-chorus"`, `<span>Chorus name</span><input name="name" value="chorus"`, `>Create chorus</button>`},
		{`action="/__actions/add-effect"`, `<select name="effectkind"`, `value="reverb"`, `<span>Name (optional)</span><input name="name" value=""`, `>Add return</button>`},
	} {
		for _, want := range wants {
			if !strings.Contains(forms[i], want) {
				t.Errorf("form %d missing %s: %s", i, want, forms[i])
			}
		}
		if strings.Contains(forms[i], "data-mixer-show") || strings.Contains(forms[i], "data-mixer-meter") {
			t.Errorf("view control inside form %d", i)
		}
	}
}

// The panel page at "/" has no Device dock, so every control stays under its strip.
func TestMixerPanelPageKeepsEveryControlUnderItsStrip(t *testing.T) {
	html := mixerHTML(t, mixerFixture(2), "/?panel=mixer")
	for _, want := range []string{`class="mixer-control"`, `name="path" value="keys.out"`, `name="path" value="keys.send.echo"`, `aria-label="keys insert chain"`, "Add vocal chain", `name="track" value="bass"`,
		`id="effect-echo"`, `name="path" value="echo.feedback"`, `id="effect-tone"`, `name="path" value="tone.hp"`, `id="effect-wide"`, `name="action" value="add-chorus"`, `value="/?panel=mixer"`,
		`data-reason="CICADA-UNSUPPORTED: bus music pan is not implemented"`, "<strong>Not available</strong>", "Bus music pan is not implemented."} {
		if !strings.Contains(html, want) {
			t.Errorf("panel page missing %s", want)
		}
	}
	for _, path := range []string{"keys.mute", "keys.solo"} {
		if got := strings.Count(html, `name="path" value="`+path+`"`); got != 1 {
			t.Errorf("%s has %d forms, want only its toggle button", path, got)
		}
	}
	// The exact level field stays beside the fader, which posts the same path.
	if got := strings.Count(html, `name="path" value="keys.level"`); got != 2 {
		t.Errorf("keys.level has %d forms, want the fader and the field", got)
	}
	for _, banned := range []string{"data-mixer-select", "data-mixer-show", `href="/studio`, ": CICADA-UNSUPPORTED"} {
		if strings.Contains(html, banned) {
			t.Errorf("panel page should not hold %s", banned)
		}
	}
}

// TestShellReadsTheMixerOncePerRequest renders every panel that shows mixer
// state (Mixer strips, Arrange rail, Device dock) and counts the backend reads.
func TestShellReadsTheMixerOncePerRequest(t *testing.T) {
	p := &project.Project{Title: "T", TempoMilli: 120000, Key: project.Key{Scale: "minor"}, Tracks: []project.Track{{ID: "voice", Kind: "audio"}},
		Arrange: &project.Arrangement{Placements: []project.Placement{{ID: "voice-1", Track: "voice", Content: "p", LengthTicks: 3840}}}}
	state := mixerState{Edition: 2, Revision: "r1", Master: mixerStrip{ID: "master", Name: "Master"},
		Tracks: []mixerStrip{{ID: "voice", Name: "voice", Fields: map[string]mixerField{"mute": toggleField("voice.mute", false), "solo": toggleField("voice.solo", false)}}}}
	var mu sync.Mutex
	reads := 0
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "r1", Edition: 2, Filename: "t.cicada", Valid: true, Project: p})
		case "/api/mixer":
			mu.Lock()
			reads++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(state)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(backendServer.Close)
	backend, err := newBackend(backendServer.URL)
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
	body := getPage(t, client, app.URL+"/studio?tab=mixer&deviceTrack=voice&dock=device&placement=voice-1")
	if !strings.Contains(body, `data-meter="track:voice"`) || !strings.Contains(body, `class="rail-toggles"`) {
		t.Fatalf("the page should show the Mixer strip and the Arrange rail buttons")
	}
	mu.Lock()
	if reads != 1 {
		t.Errorf("one page render read /api/mixer %d times, want 1", reads)
	}
	reads = 0
	mu.Unlock()
	// A full page renders Mixer and Device too; an edit can render just the
	// header. Count backend reads for that shell region on the Session tab.
	page := shellPage{view: workspace{Project: p, mixer: func() (mixerState, error) {
		var state mixerState
		err := backend.call(context.Background(), http.MethodGet, "/api/mixer", nil, &state)
		return state, err
	}}, query: url.Values{"tab": {"session"}, "deviceTrack": {"voice"}}}
	header, _, ok := page.renderRegion("dock-header")
	if !ok || !strings.Contains(header, "voice · audio") {
		t.Errorf("session header does not name the track: %s", header)
	}
	mu.Lock()
	defer mu.Unlock()
	if reads != 0 {
		t.Errorf("session render with a track header read /api/mixer %d times, want 0", reads)
	}
}

// The echo highpass is off until set, and off is no number: the echo panel
// shows "off" for it, not <nil>, and shows the cutoff once it is set.
func TestEchoPanelShowsHighpassOffAndSet(t *testing.T) {
	s := &studioApp{}
	descriptor, ok := paramdefs.Lookup("fx.delay.highpass")
	if !ok {
		t.Fatal("no fx.delay.highpass row")
	}
	unset := gosx.RenderHTML(s.mixerField(workspace{Edition: 2}, "token", "highpass", mixerField{Path: "echo.highpass", Value: nil, Supported: true, Descriptor: descriptor}))
	if strings.Contains(unset, "nil") || !strings.Contains(unset, `value="off"`) {
		t.Fatalf("unset highpass: %s", unset)
	}
	set := gosx.RenderHTML(s.mixerField(workspace{Edition: 2}, "token", "highpass", mixerField{Path: "echo.highpass", Value: 300.0, Supported: true, Descriptor: descriptor}))
	if !strings.Contains(set, `value="300"`) || !strings.Contains(set, "(Hz)") {
		t.Fatalf("set highpass: %s", set)
	}
}
