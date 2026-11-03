package main

import (
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func TestModulationDockShowsSupportedRoutesAndSourceControls(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern p { 1 . }\nscene main { bass = p }\nsong { main }\nlfo drift { rate = 1/4 }\nmod drift -> bass.pan depth = 0.2\n"))
	if score == nil {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	page := gosx.RenderHTML(new(studioApp).modulationDock(workspace{Project: p, Revision: "r", SourceRevision: "s"}, "csrf"))
	for _, text := range []string{"Modulation", "drift", "1 beat per cycle", "bass.pan", "Save depth", "Remove route", "Add source", "Add route", `name="source_revision" value="s"`} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing %q in %s", text, page)
		}
	}
	if strings.Contains(page, `<option value="bass.mute"`) {
		t.Fatal("offered an unroutable toggle")
	}
}

func TestModulationPeriodsUseMusicalTime(t *testing.T) {
	for ticks, want := range map[int64]string{3840: "1 bar per cycle", 30720: "8 bars per cycle", 960: "1 beat per cycle", 1920: "2 beats per cycle", 240: "1/4 of a beat per cycle"} {
		if got := modulationPeriod(ticks); got != want {
			t.Errorf("%d: got %q, want %q", ticks, got, want)
		}
	}
}

func TestModulationFormsCarryRevisionAndRouteIdentity(t *testing.T) {
	address, client, requests := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"seen"}, "action": {"mod-route"}, "operation": {"set"}, "source": {"drift"}, "target": {"bass.pan"}, "mode": {"add"}, "depth": {"0.4"}}
	response := post(t, client, address+"/__actions/automation", form, false, address)
	if response.StatusCode != 303 || len(*requests) != 1 {
		t.Fatalf("status %d requests %+v", response.StatusCode, *requests)
	}
	value, ok := (*requests)[0]["value"].(map[string]any)
	if !ok || (*requests)[0]["source_revision"] != "seen" || value["operation"] != "set" || value["source"] != "drift" || value["target"] != "bass.pan" || value["mode"] != "add" || value["depth"] != "0.4" {
		t.Fatalf("route request %+v", *requests)
	}
}
