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
	for _, text := range []string{"Modulation", "drift", "bass.pan", "Save depth", "Remove route", "Add source", "Add route", `name="source_revision" value="s"`} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing %q in %s", text, page)
		}
	}
	if strings.Contains(page, `<option value="bass.mute"`) {
		t.Fatal("offered an unroutable toggle")
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
