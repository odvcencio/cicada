package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/project"
)

func TestLibraryVariationUsesAValidNameAndKeepsItsSoundAndScene(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}, {ID: "upper", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "motif.played", Kind: "notes", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, EndTick: 720, Velocity: 90}}}}}, Clips: []project.Clip{{Name: "motif-played-2"}}, Scenes: []project.Scene{{ID: "main"}, {ID: "ending"}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=patterns&pattern=motif.played&track=upper&scene=ending")
	management := regexp.MustCompile(`(?s)<details[^>]*data-pattern-management="motif.played".*?</details>`).FindString(page)
	for _, want := range []string{`open`, `id="pattern-variation"`, `data-pattern-variation="motif.played"`, `name="newName" value="motif-played-3"`, `name="track" value="upper"`, `name="source_revision" value="all-sources"`} {
		if !strings.Contains(management, want) {
			t.Fatal("missing library variation control", want)
		}
	}
	fields := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"all-sources"}, "action": {"duplicate"}, "pattern": {"motif.played"}, "newName": {"reply"}, "track": {"upper"}, "scene": {"ending"}}
	r := post(t, client, address+"/__actions/pattern", fields, false, address)
	defer r.Body.Close()
	if r.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["track"] != "upper" || (*edits)[0]["source_revision"] != "all-sources" {
		t.Fatal("variation lost its sound context or receipt", r.StatusCode, *edits)
	}
	target, _ := url.Parse(r.Header.Get("Location"))
	if target.Query().Get("pattern") != "reply" || target.Query().Get("track") != "upper" || target.Query().Get("scene") != "ending" {
		t.Fatal("variation lost editing context", target)
	}
}

type variationDeadlineTransport func(*http.Request) (*http.Response, error)

func (f variationDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVariationBackendExtendsOnlyTheCopyDeadline(t *testing.T) {
	b, err := newBackend("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	var remaining time.Duration
	b.client.Transport = variationDeadlineTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("backend request lost its deadline")
		}
		remaining = time.Until(deadline)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	for _, action := range []string{"duplicate", "performed-note"} {
		response, err := b.request(context.Background(), http.MethodPost, "/api/pattern", map[string]any{"action": action})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if action == "duplicate" && (remaining < 55*time.Second || remaining > 61*time.Second) || action != "duplicate" && (remaining < 10*time.Second || remaining > 16*time.Second) {
			t.Fatal("wrong effective request deadline", action, remaining)
		}
	}
}
