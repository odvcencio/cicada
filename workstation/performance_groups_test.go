package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func TestSharedEditorRetainsSortedSelectionAndBoundsLargeLanes(t *testing.T) {
	p := project.Pattern{ID: "phrase", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, EndTick: 120, Velocity: 100, Group: 1}}, Groups: []project.ControlGroup{{Name: "touch"}}}}
	for tick := int64(0); tick < 1024; tick++ {
		p.Performance.Groups[0].Points = append(p.Performance.Groups[0].Points, project.GroupExpression{Tick: tick, Pressure: .25, Timbre: .5})
	}
	ctx := &server.Context{Request: httptest.NewRequest("GET", "http://studio/?group=0&shared_point=0&shared_tick=129", nil)}
	page := gosx.RenderHTML((&studioApp{}).sharedGestures(ctx, workspace{Revision: "current"}, "csrf", p, 0))
	for _, want := range []string{`Shared gestures`, `name="group_index" value="0"`, `name="index" value="129"`, `Tick in loop`, `value="129"`, `Points 129–192 of 1024`, `release tails follow this lane`, `Bind note`, `name="group"`, `overview keeps extrema`} {
		if !strings.Contains(page, want) {
			t.Fatal("missing shared control", want)
		}
	}
	if strings.Count(page, "<tr>") != 65 || len(page) > 120000 || strings.Contains(page, "vibrato_rate_hz") {
		t.Fatal("shared lane DOM was unbounded or replaced member vibrato", len(page))
	}
}

func TestSharedActionsKeepPrecisionAndRejectInvalidOrStaleForms(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "pattern": {"p"}, "action": {"shared-gesture"}, "group_index": {"1"}, "index": {"3"}, "note_index": {"2"}, "tick": {"123"}, "pitch_cents": {"-123.125"}, "pressure": {"0.37"}, "timbre": {"0.81"}}
	r := post(t, client, address+"/__actions/pattern", form, false, address)
	if r.StatusCode != 303 || len(*edits) != 1 {
		t.Fatal(r.StatusCode)
	}
	destination, err := url.Parse(r.Header.Get("Location"))
	if err != nil || destination.Query().Get("group") != "1" || destination.Query().Get("shared_tick") != "123" || destination.Query().Get("note") != "2" || destination.Query().Has("shared_point") {
		t.Fatal("shared selection did not follow the moved point", destination, err)
	}
	point := (*edits)[0]["sharedExpression"].(map[string]any)
	if point["pitch_cents"] != -123.125 || point["group_index"] != float64(1) {
		t.Fatal("shared edit lost precision or lane identity", point)
	}
	form.Set("pressure", "NaN")
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("invalid shared control reached the backend")
	}
	form.Set("pressure", "0.37")
	form.Set("revision", "old")
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 409 {
		t.Fatal("stale shared edit changed the score")
	}
}
