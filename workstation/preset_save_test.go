package main

import (
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPresetSaveRequiresFullSourceRevision(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "bass", Kind: "acid"}, {ID: "lead", Kind: "piano"}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=instruments")
	for _, want := range []string{`data-preset-save`, `name="source_revision" value="all-sources"`, `value="bass"`, `value="lead"`, "Save preset"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing preset control %q", want)
		}
	}
	fields := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"all-sources"}, "track": {"bass"}, "name": {"bright"}}
	r := post(t, client, address+"/__actions/preset-save", fields, false, address)
	r.Body.Close()
	if r.StatusCode != 303 || len(*edits) != 1 {
		t.Fatalf("save: %d, edits %+v", r.StatusCode, *edits)
	}
	if edit := (*edits)[0]; edit["track"] != "bass" || edit["name"] != "bright" || edit["source_revision"] != "all-sources" {
		t.Fatalf("lost preset receipt: %+v", edit)
	}
	fields.Del("source_revision")
	r = post(t, client, address+"/__actions/preset-save", fields, false, address)
	r.Body.Close()
	if len(*edits) != 1 {
		t.Fatal("missing source revision reached the backend")
	}
}
