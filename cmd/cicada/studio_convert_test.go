package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/rendercheck"
)

// countingRender counts the renders that reach the real check behind the cache.
type countingRender struct {
	inner edits.RenderCheck
	calls atomic.Int32
}

func (c *countingRender) Hash(source []byte) (string, error) {
	c.calls.Add(1)
	return c.inner.Hash(source)
}

const convertSongScore = "title \"Convert\"\ntempo 130\n\ntrack bass acid {}\n\npattern pulse acid {\n  1 . . . 5 . . . 1 . . . 7 . . .\n}\n\nscene main { bass = pulse }\nscene break { bass = pulse }\n\nsong { main*2 break*2 }\n"

const convertDrumSongScore = "title \"Convert\"\ntempo 130\n\ntrack drums drums {}\n\npattern beat drums {\n  bd: X... .... X... ....\n}\n\npattern fill drums {\n  sd: X... .... X... ....\n}\n\nscene main { drums = beat }\nscene break { drums = fill }\n\nsong { main*2 break*2 }\n"

func convertEnvelope(source string, dryRun bool) edits.Envelope {
	return edits.Envelope{Version: 1, Revision: studioRevision([]byte(source)), Author: "tester", Session: "convert-test", DryRun: dryRun, Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
}

func TestStudioConvertDryRunRefusalNamesCauseAndCachesRender(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, handler, path := proposalFixture(t, convertDrumSongScore)
	counter := &countingRender{inner: rendercheck.New(path)}
	s.renderCheck = rendercheck.Cached(path, counter)
	for call := 1; call <= 2; call++ {
		r := studioCall(t, handler, "/api/intents", convertEnvelope(convertDrumSongScore, true))
		var reply struct {
			Convertible *bool
			Cause       string
			DryRun      bool
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.Convertible == nil || *reply.Convertible || !reply.DryRun ||
			reply.Cause != "drums track drums starts a new placement at bar 3, which renders differently from the song" {
			t.Fatalf("call %d: %d %s", call, r.Code, r.Body.String())
		}
		// The first call renders the song and its conversion once each; the
		// second call at the same revision renders nothing.
		if got := counter.calls.Load(); got != 2 {
			t.Fatalf("call %d: %d renders, want 2", call, got)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != convertDrumSongScore || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("a refused dry run changed the score or history")
	}
	// Without a dry run the refusal is an error and the score stays.
	r := studioCall(t, handler, "/api/intents", convertEnvelope(convertDrumSongScore, false))
	if r.Code != 422 || !strings.Contains(r.Body.String(), "starts a new placement at bar 3") {
		t.Fatalf("committed refusal: %d %s", r.Code, r.Body.String())
	}
}

func TestStudioConvertCommitsOneHistoryEntryAndUndoRestoresSong(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, handler, path := proposalFixture(t, convertSongScore)
	r := studioCall(t, handler, "/api/intents", convertEnvelope(convertSongScore, true))
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"convertible":true`) || !strings.Contains(r.Body.String(), "arrange") {
		t.Fatalf("dry run: %d %s", r.Code, r.Body.String())
	}
	r = studioCall(t, handler, "/api/intents", convertEnvelope(convertSongScore, false))
	if r.Code != 200 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	converted, _ := os.ReadFile(path)
	want := "cicada 2\n" + strings.Replace(convertSongScore, "song { main*2 break*2 }", "arrange {\n  place main-1-bass bass pulse {\n    at = @1.1.1 length = 4bars\n  }\n}", 1)
	if string(converted) != want {
		t.Fatalf("converted:\n%s\nwant:\n%s", converted, want)
	}
	edits := studioHistoryEdits(t, handler)
	if len(edits) != 1 || edits[0].Label != "Song converted to arrange" {
		t.Fatalf("history: %+v", edits)
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(converted)}); r.Code != 200 {
		t.Fatalf("undo: %d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != convertSongScore {
		t.Fatalf("undo restored:\n%s", got)
	}
}

func TestStudioConvertWithoutRenderCheckIsRefusedNotWritten(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, handler, path := proposalFixture(t, convertSongScore)
	s.renderCheck = nil
	r := studioCall(t, handler, "/api/intents", convertEnvelope(convertSongScore, true))
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"convertible":false`) || !strings.Contains(r.Body.String(), "render check unavailable") {
		t.Fatalf("dry run: %d %s", r.Code, r.Body.String())
	}
	if r := studioCall(t, handler, "/api/intents", convertEnvelope(convertSongScore, false)); r.Code != 422 {
		t.Fatalf("commit: %d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != convertSongScore {
		t.Fatal("score changed")
	}
}
