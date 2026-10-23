package main

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestCreatePatternActionSelectsTheNewGridAndPreservesTrackContext(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"create"}, "newName": {"glow"}, "track": {"keys"}, "scene": {"main"}, "length": {"16"}, "createFor": {"keys"}, "__gosx_return_to": {"/?panel=instruments"}}
	response := post(t, client, address+"/__actions/pattern", form, false, address)
	target, _ := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != 303 || target.Query().Get("pattern") != "glow" || target.Query().Get("track") != "keys" || target.Query().Get("scene") != "main" {
		t.Fatalf("creation lost the new grid or target: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	if len(*edits) != 1 || (*edits)[0]["newName"] != "glow" || (*edits)[0]["length"] != float64(16) || (*edits)[0]["track"] != "keys" || (*edits)[0]["scene"] != "main" {
		t.Fatalf("creation command: %+v", *edits)
	}
	for _, invalid := range []string{"0", "65", "1.5", ""} {
		form.Set("length", invalid)
		response := post(t, client, address+"/__actions/pattern", form, true, address)
		if response.StatusCode != 422 || len(*edits) != 1 {
			t.Fatalf("invalid length %q reached the service: %d", invalid, response.StatusCode)
		}
	}
	form.Set("length", "16")
	form.Set("revision", "stale")
	if response := post(t, client, address+"/__actions/pattern", form, true, address); response.StatusCode != 409 {
		t.Fatal("creation ignored the source revision")
	}
}

func TestSceneBindingActionSupportsStopAndCarry(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"bind"}, "pattern": {"glow"}, "track": {"keys"}, "scene": {"breakdown"}}
	for i, mode := range []string{"play", "off", "keep"} {
		form.Set("binding", mode)
		response := post(t, client, address+"/__actions/pattern", form, false, address)
		want := mode
		if mode == "play" {
			want = "glow"
		}
		target, _ := url.Parse(response.Header.Get("Location"))
		if response.StatusCode != 303 || (*edits)[i]["pattern"] != want || (*edits)[i]["action"] != "bind" || target.Query().Get("track") != "keys" || target.Query().Get("scene") != "breakdown" {
			t.Fatalf("binding %s: %d target=%q %+v", mode, response.StatusCode, response.Header.Get("Location"), *edits)
		}
	}
	form.Set("binding", "invalid")
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 422 || len(*edits) != 3 {
		t.Fatal("an invalid scene behavior reached the service")
	}
}

func TestPatternActionsUseOneBasedControlsAndKeepTheInspectedStep(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "action": {"pitch"}, "pattern": {"p"}, "step": {"4"}, "pitch": {"72"}, "octave": {"3"}, "__gosx_return_to": {"/?panel=patterns&pattern=p&step=1"}}
	response := post(t, client, address+"/__actions/pattern", form, false, address)
	if response.StatusCode != 303 || response.Header.Get("Location") != patternURL("p", 4, "", "3") {
		t.Fatalf("native edit lost the selected step: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	if len(*edits) != 1 || (*edits)[0]["step"] != float64(3) || (*edits)[0]["pitch"] != float64(72) {
		t.Fatalf("native pattern command: %+v", *edits)
	}
	form.Set("step", "4.5")
	response = post(t, client, address+"/__actions/pattern", form, true, address)
	if response.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("fractional step reached the audio service")
	}
	form.Set("step", "4")
	form.Set("revision", "old")
	response = post(t, client, address+"/__actions/pattern", form, true, address)
	if response.StatusCode != 409 {
		t.Fatal("stale pattern edit was not rejected")
	}
}

func TestPatternRangeTimingAndVariationActionContracts(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "pattern": {"p"}, "action": {"range"}, "operation": {"copy"}, "first": {"1"}, "last": {"4"}, "target": {"3"}, "amount": {"12"}, "lane": {"bd"}}
	response := post(t, client, address+"/__actions/pattern", form, true, address)
	if response.StatusCode != 303 {
		t.Fatalf("range: %d", response.StatusCode)
	}
	selection := (*edits)[0]["range"].(map[string]any)
	if selection["first"] != float64(0) || selection["last"] != float64(3) || selection["target"] != float64(2) || selection["amount"] != float64(12) {
		t.Fatalf("range indexing: %+v", selection)
	}
	form.Set("action", "settings")
	form.Set("swing", "57.25")
	form.Set("gate", "60")
	form.Set("transpose", "-12")
	response = post(t, client, address+"/__actions/pattern", form, true, address)
	settings := (*edits)[1]["settings"].(map[string]any)
	if response.StatusCode != 303 || settings["swing100"] != float64(5725) || settings["gate"] != float64(60) || settings["transpose"] != float64(-12) {
		t.Fatalf("settings: %+v", settings)
	}
	form.Set("action", "duplicate")
	form.Set("newName", "variation")
	response = post(t, client, address+"/__actions/pattern", form, true, address)
	var result struct {
		Redirect string `json:"redirect"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Redirect != patternURL("variation", 1, "", "") {
		t.Fatalf("new variation was not opened: %s", result.Redirect)
	}
}

func TestPerformanceActionsKeepTickPrecisionAndSelection(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	target := patternURL("p", 1, "", "") + "&note=2"
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "pattern": {"p"}, "action": {"performed-note"}, "index": {"2"}, "pitch": {"64"}, "tick": {"961"}, "end_tick": {"1099511627775"}, "velocity": {"105"}, "__gosx_return_to": {target}}
	r := post(t, client, address+"/__actions/pattern", form, false, address)
	if r.StatusCode != 303 || r.Header.Get("Location") != target || len(*edits) != 1 {
		t.Fatalf("performance selection lost: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	n := (*edits)[0]["performanceNote"].(map[string]any)
	if n["tick"] != float64(961) || n["end_tick"] != float64(1099511627775) || n["velocity"] != float64(105) {
		t.Fatal("performance action changed exact tick positions", n)
	}
	form.Set("tick", "1.5")
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("fractional ticks reached the service")
	}
}

func TestProjectActionKeepsMilliBPMPrecisionAndSourceRevision(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "title": {"Tokyo Studio"}, "tempo": {"127.125"}, "root": {"c#"}, "scale": {"minor"}}
	response := post(t, client, address+"/__actions/project", form, true, address)
	if response.StatusCode != 303 || len(*edits) != 1 {
		t.Fatalf("project action: %d", response.StatusCode)
	}
	metadata := (*edits)[0]["metadata"].(map[string]any)
	if metadata["tempoMilli"] != float64(127125) || metadata["root"] != "c#" || (*edits)[0]["revision"] != "current" {
		t.Fatalf("project settings: %+v", metadata)
	}
	form.Set("tempo", "127.1255")
	response = post(t, client, address+"/__actions/project", form, true, address)
	if response.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("tempo precision was silently rounded")
	}
}
