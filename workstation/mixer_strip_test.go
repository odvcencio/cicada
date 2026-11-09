package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestMixerPanSubmitsOnChangeAndResetsToCentre(t *testing.T) {
	s := &studioApp{}
	pan := mixerField{Path: "keys.pan", Value: -0.35, Supported: true, Descriptor: paramdefs.Descriptor{Min: -1, Max: 1}}
	html := gosx.RenderHTML(s.mixerPan(workspace{Edition: 2}, "token", "keys", pan))
	for _, want := range []string{`type="range"`, `min="-1"`, `max="1"`, `step="0.01"`, `value="-0.35"`, `aria-label="keys pan"`, `data-gosx-submit-on="change"`, `name="path" value="keys.pan"`, `name="valueType" value="number"`, `>L35<`, `aria-label="keys pan to centre"`, `name="value" value="0"`} {
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

// mixerPageHTML renders the Mixer for a backend whose /api/mixer answers with
// the given edition: two tracks, a music bus and the master.
func mixerPageHTML(t *testing.T, edition int) string {
	t.Helper()
	strip := func(id string, muted bool) mixerStrip {
		return mixerStrip{ID: id, Name: id, Fields: map[string]mixerField{
			"level": {Path: id + ".level", Value: -6.0, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6, Unit: "dB"}},
			"pan":   {Path: id + ".pan", Value: 0.25, Supported: true, Descriptor: paramdefs.Descriptor{Min: -1, Max: 1}},
			"mute":  toggleField(id+".mute", muted),
			"solo":  toggleField(id+".solo", false),
		}}
	}
	state := mixerState{Edition: edition, Revision: "r1", Tracks: []mixerStrip{strip("keys", true), strip("bass", false)}, Buses: []mixerStrip{strip("music", false)}, Master: strip("master", false),
		Effects: []mixerReturn{{Name: "room", Meter: "b"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(state) }))
	t.Cleanup(server.Close)
	backend, err := newBackend(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &studioApp{backend: backend}
	return gosx.RenderHTML(app.mixer(&gosxserver.Context{Request: httptest.NewRequest(http.MethodGet, "/studio?tab=mixer", nil)}, workspace{}, "token"))
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
		t.Error("edition 1 Set forms keep the upgrade confirmation")
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
