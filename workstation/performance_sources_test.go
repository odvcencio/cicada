package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPerformedFormsCarryProjectReceiptsAndInlineFeedback(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "phrase", Kind: "notes", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90, Group: 1, Expressions: []project.PerformedExpression{{Value: project.NoteExpression{Timbre: .5}}}}}, Groups: []project.ControlGroup{{Name: "touch", Points: []project.GroupExpression{{Timbre: .5}}}}}}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "whole-project", Valid: true, Project: p})
	page := getPage(t, client, address+"/studio?dock=clip&pattern=phrase&note=0&track=keys&region=detail")
	forms := regexp.MustCompile(`(?s)<form\b[^>]*>.*?</form>`).FindAllString(page, -1)
	for _, c := range []struct {
		operation string
		fields    map[string]string
	}{
		{"performed-note", map[string]string{"index": "0", "tick": "17", "end_tick": "720", "pitch": "64", "velocity": "95"}},
		{"performance-length", map[string]string{"length_ticks": "7680"}},
		{"performed-gesture", map[string]string{"note_index": "0", "index": "0", "tick": "0", "pitch_cents": "75", "pressure": "0.6", "timbre": "0.4", "vibrato_depth_cents": "14", "vibrato_rate_hz": "6"}},
		{"performance-group", map[string]string{"index": "0", "name": "swell"}},
		{"performance-binding", map[string]string{"note_index": "0", "group": "0"}},
		{"shared-gesture", map[string]string{"group_index": "0", "index": "0", "tick": "0", "pitch_cents": "150", "pressure": "0.7", "timbre": "0.6"}},
	} {
		t.Run(c.operation, func(t *testing.T) {
			found := false
			for _, f := range forms {
				if strings.Contains(f, `name="action" value="`+c.operation+`"`) {
					found = true
					for _, want := range []string{`name="source_revision" value="whole-project"`, `name="track" value="keys"`, `class="form-status"`, `role="status"`} {
						if !strings.Contains(f, want) {
							t.Fatal("performed form missing context or feedback", want)
						}
					}
				}
			}
			if !found {
				t.Fatal("missing form", c.operation)
			}
			data := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"whole-project"}, "action": {c.operation}, "pattern": {"phrase"}, "__gosx_return_to": {"/studio?dock=clip&pattern=phrase&note=0&track=keys&region=detail"}}
			for key, value := range c.fields {
				data.Set(key, value)
			}
			previous := len(*edits)
			r := post(t, client, address+"/__actions/pattern", data, true, address)
			if r.StatusCode != 303 || len(*edits) != previous+1 || (*edits)[previous]["source_revision"] != "whole-project" {
				t.Fatal("performed action lost its source receipt", r.StatusCode, *edits)
			}
		})
	}
}
