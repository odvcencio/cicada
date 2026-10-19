package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func TestGestureEditorKeepsSortedSelectionAndBoundsLargeTakes(t *testing.T) {
	p := project.Pattern{ID: "phrase", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, EndTick: 2000, Velocity: 90}}}}
	for tick := int64(0); tick < 1024; tick++ {
		p.Performance.Notes[0].Expressions = append(p.Performance.Notes[0].Expressions, project.PerformedExpression{Tick: tick, Value: project.NoteExpression{Pressure: .25, Timbre: .5}})
	}
	ctx := &server.Context{Request: httptest.NewRequest("GET", "http://studio/?gesture=0&gesture_tick=129", nil)}
	page := gosx.RenderHTML((&studioApp{}).performanceGestures(ctx, workspace{Revision: "current"}, "csrf", p, 0))
	for _, want := range []string{`name="index" value="129"`, `name="note_index" value="0"`, `name="tick" value="129"`, `name="vibrato_rate_hz"`, `Points 129–192 of 1024`, `role="img"`, `overview keeps extrema`, `Remove gesture point`, `aria-current="true"`} {
		if !strings.Contains(page, want) {
			t.Fatal("missing gesture editor control", want)
		}
	}
	if strings.Count(page, "<tr>") != 65 || len(page) > 120000 {
		t.Fatal("large take rendered unbounded DOM", len(page), strings.Count(page, "<tr>"))
	}
	points := p.Performance.Notes[0].Expressions
	points[17].Value.Pressure = 1
	points[501].Value.PitchCents = -100
	indices := gestureOverviewIndices(points, 129)
	if len(indices) > 512 {
		t.Fatal("unbounded overview", len(indices))
	}
	for _, want := range []int{0, 17, 129, 501, 1023} {
		found := false
		for _, i := range indices {
			found = found || i == want
		}
		if !found {
			t.Fatal("overview lost selected point or extremum", want)
		}
	}
	if gestureSelection(points, "999999", "") != len(points) || gestureSelection(points, "-3", "") != 0 {
		t.Fatal("invalid selection escaped bounds")
	}
}

func TestReactiveGestureSelectionFollowsMovedPoint(t *testing.T) {
	address, client, _ := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=patterns"))
	response, result := reactivePost(t, client, address, "pattern", map[string]string{
		"csrf_token": csrf, "revision": "current", "action": "performed-gesture", "pattern": "p", "note_index": "2", "index": "1", "tick": "711", "pitch_cents": "212.5", "pressure": "0.7", "timbre": "0.9", "vibrato_depth_cents": "14", "vibrato_rate_hz": "6.75",
		"__cicada_location": "/?panel=patterns&pattern=p&note=2&gesture=1#note-gestures",
	})
	var projection workspaceProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	destination, err := url.Parse(projection.Location)
	if response.StatusCode != 200 || !result.OK || err != nil || destination.Query().Get("gesture_tick") != "711" || destination.Query().Has("gesture") || destination.Query().Get("note") != "2" {
		t.Fatal("reactive edit kept an obsolete sorted index", projection.Location, err)
	}
}

func TestGestureActionPreservesPrecisionAndRejectsStaleOrInvalidForms(t *testing.T) {
	address, client, edits := testApp(t)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	form := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "pattern": {"p"}, "action": {"performed-gesture"}, "note_index": {"2"}, "index": {"3"}, "tick": {"123"}, "pitch_cents": {"-123.5"}, "pressure": {"0.37"}, "timbre": {"0.81"}, "vibrato_depth_cents": {"14.5"}, "vibrato_rate_hz": {"7.25"}, "__gosx_return_to": {patternURL("p", 1, "", "") + "&note=2&gesture=3#note-gestures"}}
	r := post(t, client, address+"/__actions/pattern", form, false, address)
	if r.StatusCode != 303 || len(*edits) != 1 {
		t.Fatal(r.StatusCode)
	}
	destination, err := url.Parse(r.Header.Get("Location"))
	if err != nil || destination.Query().Get("note") != "2" || destination.Query().Get("gesture_tick") != "123" || destination.Query().Has("gesture") || destination.Fragment != "note-gestures" {
		t.Fatal("selection did not follow sorted point", destination, err)
	}
	point := (*edits)[0]["performanceExpression"].(map[string]any)
	if point["note_index"] != float64(2) || point["pitch_cents"] != -123.5 || point["vibrato_rate_hz"] != 7.25 {
		t.Fatal(point)
	}
	for _, bad := range []struct{ key, value string }{{"tick", "1.5"}, {"pressure", "NaN"}, {"vibrato_rate_hz", "+Inf"}} {
		previous := form.Get(bad.key)
		form.Set(bad.key, bad.value)
		if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 422 || len(*edits) != 1 {
			t.Fatal("invalid controller reached service", bad, r.StatusCode)
		}
		form.Set(bad.key, previous)
	}
	form.Set("revision", "old")
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 409 || len(*edits) != 2 {
		t.Fatal("stale gesture changed the score")
	}
	form.Set("revision", "current")
	form.Set("intent", "remove")
	for _, key := range []string{"tick", "pitch_cents", "pressure", "timbre", "vibrato_depth_cents", "vibrato_rate_hz"} {
		form.Del(key)
	}
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 303 || len(*edits) != 3 {
		t.Fatal("remove required discarded controls", r.StatusCode)
	}
}
