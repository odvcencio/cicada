package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPlacementVariationFormsCarryReceiptsAndRetainNativeFailures(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}, {ID: "voice", Kind: "audio"}}, Patterns: []project.Pattern{{ID: "parent", Kind: "notes", Steps: 4}}, Clips: []project.Clip{{Name: "take"}}, Arrange: &project.Arrangement{Placements: []project.Placement{{ID: "first", Track: "keys", Content: "parent", LengthTicks: 3840}, {ID: "audio", Track: "voice", Content: "take", LengthTicks: 3840}}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=session&placement=first")
	if strings.Contains(page, `data-placement-variation="audio"`) {
		t.Fatal("audio recording offered as a note-pattern variation")
	}
	variation := regexp.MustCompile(`(?s)<section[^>]*data-placement-variation="first".*?</section>`).FindString(page)
	for _, want := range []string{"Vary this phrase", "Make independent variation", `name="source_revision" value="all-sources"`, `name="newName" value="parent-2"`} {
		if !strings.Contains(variation, want) {
			t.Fatal("missing variation control", want)
		}
	}
	fields := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"stale"}, "source_revision": {"all-sources"}, "action": {"vary"}, "name": {"first"}, "newName": {"my-idea"}, "__gosx_return_to": {"/?panel=session&placement=first"}}
	r := post(t, client, address+"/__actions/arrange", fields, false, address)
	defer r.Body.Close()
	if r.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["action"] != "vary" || (*edits)[0]["name"] != "first" || (*edits)[0]["newName"] != "my-idea" || (*edits)[0]["source_revision"] != "all-sources" {
		t.Fatal("variation action lost the placement, name or source receipt", r.StatusCode, *edits)
	}
	page = getPage(t, client, address+r.Header.Get("Location"))
	variation = regexp.MustCompile(`(?s)<section[^>]*data-placement-variation="first".*?</section>`).FindString(page)
	for _, want := range []string{`name="newName" value="my-idea"`, `role="status"`, "score changed"} {
		if !strings.Contains(variation, want) {
			t.Fatal("failed native variation lost its context", want)
		}
	}
}
