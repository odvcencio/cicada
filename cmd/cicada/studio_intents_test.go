package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/project"
)

func TestStudioIntentsCommitDryRunAndNoop(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, handler, path := proposalFixture(t, studioMixerScore)
	env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(studioMixerScore)), Author: "tester", Session: "intents-test", DryRun: true, Intents: []edits.Intent{
		&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)},
		&edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)},
	}}
	before := noopFiles(t, filepath.Dir(path))
	r := studioCall(t, handler, "/api/intents", env)
	var reply struct {
		Diff, Revision string
		Valid, DryRun  bool
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &reply) != nil || !reply.Valid || !reply.DryRun || reply.Revision != env.Revision || !strings.Contains(reply.Diff, "@@") {
		t.Fatalf("dry run: %d %s", r.Code, r.Body.String())
	}
	if !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("dry run wrote files or history")
	}
	env.DryRun = false
	r = studioCall(t, handler, "/api/intents", env)
	after := assertProposalResponseMatchesDisk(t, path, r)
	records, skipped, err := editlog.ReadLog(editlog.Path(path))
	wantRaw, _ := env.RawIntents()
	if err != nil || skipped != 0 || len(records) != 1 || len(studioHistoryEdits(t, handler)) != 1 || records[0].Author != env.Author || records[0].Session != env.Session || records[0].Parent != env.Revision || records[0].Revision != studioRevision(after) || !reflect.DeepEqual(records[0].Intents, wantRaw) {
		t.Fatalf("commit log: %+v, skipped=%d, err=%v", records, skipped, err)
	}
	env.Revision = studioRevision(after)
	before = noopFiles(t, filepath.Dir(path))
	r = studioCall(t, handler, "/api/intents", env)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"unchanged":true`) || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) || len(studioHistoryEdits(t, handler)) != 1 {
		t.Fatalf("noop: %d %s", r.Code, r.Body.String())
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: env.Revision}); r.Code != 200 {
		t.Fatalf("undo: %d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != studioMixerScore {
		t.Fatal("one undo did not restore source")
	}
}

func TestStudioIntentsRejectInvalidRequests(t *testing.T) {
	_, handler, path := proposalFixture(t, studioScore)
	for _, c := range []struct {
		body, contentType, origin, message string
		status                             int
	}{
		{`{"version":1,"revision":"x","intents":[{"kind":"nope"}]}`, "application/json", "", `unknown intent kind`, 400},
		{`{"version":2,"revision":"x","intents":[]}`, "application/json", "", `unsupported envelope version`, 400},
		{`{"version":1,"intents":[]}`, "application/json", "", `missing revision`, 400},
		{`{"version":1,"revision":"x","intents":[],"write":true}`, "application/json", "", `unknown field`, 400},
		{`{"version":1,"revision":"x","intents":[{"kind":"togglestep","entity":"step:pulse/0","extra":1}]}`, "application/json", "", `unknown field`, 400},
		{`{"version":1,"revision":"x","intents":[]} {}`, "application/json", "", `expected one JSON request`, 400},
		{`{`, "application/json", "", `unexpected EOF`, 400},
		{`{}`, "text/plain", "", `expected JSON`, 415},
		{`{}`, "application/json", "http://elsewhere.invalid", `cross-origin`, 403},
	} {
		t.Run(c.message+c.body, func(t *testing.T) {
			before := noopFiles(t, filepath.Dir(path))
			req := httptest.NewRequest("POST", "/api/intents", strings.NewReader(c.body))
			req.Host = "127.0.0.1:1234"
			req.Header.Set("Content-Type", c.contentType)
			req.Header.Set("Origin", c.origin)
			r := httptest.NewRecorder()
			handler.ServeHTTP(r, req)
			if r.Code != c.status || !strings.Contains(r.Body.String(), c.message) || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) {
				t.Fatalf("request: %d %s", r.Code, r.Body.String())
			}
		})
	}
}

func TestStudioIntentsCanonicalConflictAndInvalidCandidate(t *testing.T) {
	for _, duringValidation := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "validation"}[duringValidation], func(t *testing.T) {
			s, handler, path := proposalFixture(t, studioScore)
			original := []byte(studioScore)
			external := append(bytes.Clone(original), []byte("// external change\n")...)
			if duringValidation {
				previous := studioCompile
				t.Cleanup(func() { studioCompile = previous })
				studioCompile = func(path string, source []byte, files map[string][]byte) (*project.Project, error) {
					p, err := previous(path, source, files)
					if err == nil {
						if err := os.WriteFile(path, external, 0600); err != nil {
							t.Fatal(err)
						}
					}
					return p, err
				}
			} else if err := os.WriteFile(path, external, 0600); err != nil {
				t.Fatal(err)
			}
			r := studioCall(t, handler, "/api/intents", edits.Envelope{Version: 1, Revision: studioRevision(original), Intents: []edits.Intent{&edits.ReplaceText{Source: studioScore + "// candidate\n"}}})
			var reply struct{ Error, Revision, Source, PlayingRevision string }
			if r.Code != http.StatusConflict || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.Error == "" || reply.Revision != studioRevision(external) || reply.Source != string(external) || reply.PlayingRevision != studioRevision(s.lastGoodSource) {
				t.Fatalf("conflict: %d %s", r.Code, r.Body.String())
			}
			if records, _, err := editlog.ReadLog(editlog.Path(path)); err != nil || len(records) != 0 {
				t.Fatalf("conflict logged a commit: %+v %v", records, err)
			}
			for _, entry := range studioHistoryEdits(t, handler) {
				if entry.Label != "External file change" {
					t.Fatalf("conflict recorded an edit: %+v", entry)
				}
			}
		})
	}
	_, handler, path := proposalFixture(t, studioScore)
	before := noopFiles(t, filepath.Dir(path))
	r := studioCall(t, handler, "/api/intents", edits.Envelope{Version: 1, Revision: studioRevision([]byte(studioScore)), Intents: []edits.Intent{&edits.ReplaceText{Source: "???"}}})
	if r.Code != 422 || !strings.Contains(r.Body.String(), "CICADA-SYNTAX") || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("invalid: %d %s", r.Code, r.Body.String())
	}
}

func TestStudioIntentsAuxiliaryOnlyUpgradeMatchesSequentialHTTP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, first := range []string{"setparam", "addeffect"} {
		t.Run(first, func(t *testing.T) {
			source := strings.Replace(studioScore, "track bass acid {}", "track bass acid { level = -6dB }", 1)
			_, handler, path, manifest := proposalProject(t, source, 1)
			_, sequential, seqPath, seqManifest := proposalProject(t, source, 1)
			var upgrade edits.Intent = &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true}
			if first == "addeffect" {
				upgrade = &edits.AddEffect{Name: "grit", EffectKind: "drive", ConfirmUpgrade: true}
			}
			env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(source)), DryRun: true, Intents: []edits.Intent{upgrade, &edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)}, &edits.ReplaceText{Source: source}}}
			before := noopFiles(t, filepath.Dir(path))
			r := studioCall(t, handler, "/api/intents", env)
			if r.Code != 200 || !strings.Contains(r.Body.String(), "cicada.mod") || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) {
				t.Fatalf("upgrade dry run: %d %s", r.Code, r.Body.String())
			}
			for _, intent := range env.Intents {
				current, _ := os.ReadFile(seqPath)
				r := studioCall(t, sequential, "/api/intents", edits.Envelope{Version: 1, Revision: studioRevision(current), Intents: []edits.Intent{intent}})
				assertProposalResponseMatchesDisk(t, seqPath, r)
			}
			env.DryRun = false
			r = studioCall(t, handler, "/api/intents", env)
			if got := assertProposalResponseMatchesDisk(t, path, r); string(got) != source {
				t.Fatal("auxiliary-only batch changed entry")
			}
			want, _ := os.ReadFile(seqManifest)
			got, _ := os.ReadFile(manifest)
			if !bytes.Equal(got, want) || !bytes.Contains(got, []byte("cicada 2")) || len(studioHistoryEdits(t, handler)) != 1 {
				t.Fatal("batch manifest differs from sequential HTTP")
			}
			records, _, err := editlog.ReadLog(editlog.Path(path))
			if err != nil || len(records) != 1 || len(records[0].Intents) != len(env.Intents) {
				t.Fatalf("auxiliary-only log: %+v %v", records, err)
			}
			if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: env.Revision}); r.Code != 200 {
				t.Fatalf("undo: %d %s", r.Code, r.Body.String())
			}
			if got, _ := os.ReadFile(manifest); string(got) != "project proposals\ncicada 1\n" {
				t.Fatalf("manifest undo: %q", got)
			}
		})
	}
}

func TestStudioIntentsNoopMatchesMixerResponse(t *testing.T) {
	_, handler, path := proposalFixture(t, studioMixerScore)
	before := noopFiles(t, filepath.Dir(path))
	revision := studioRevision([]byte(studioMixerScore))
	legacy := studioCall(t, handler, "/api/mixer", studioEdit{Revision: revision, Path: "bass.level", Value: json.RawMessage(`-6`)})
	r := studioCall(t, handler, "/api/intents", edits.Envelope{Version: 1, Revision: revision, Intents: []edits.Intent{&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-6`)}}})
	if legacy.Code != r.Code || !bytes.Equal(legacy.Body.Bytes(), r.Body.Bytes()) || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("no-op response differs: %s / %s", legacy.Body.String(), r.Body.String())
	}
}

func TestStudioIntentsRefuseForeignOffsets(t *testing.T) {
	entry := strings.Replace(studioScore, "pattern pulse acid steps=4 { 1 . 5 . }\n", "// preserve entry bytes\n", 1)
	path := studioTestPath(t, entry)
	root := filepath.Dir(path)
	for name, source := range map[string]string{"cicada.mod": "project foreign\ncicada 2\nentry \"score.cicada\"\nsource \"part.cicada\"\n", "part.cicada": "pattern pulse acid steps=4 { 1 . 5 . }\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	before := noopFiles(t, root)
	r := studioCall(t, s.domainRoutes(), "/api/intents", edits.Envelope{Version: 1, Revision: studioRevision([]byte(entry)), Intents: []edits.Intent{&edits.RecordTake{Recordings: []edits.Recording{{Track: "bass", Pattern: "pulse", Notes: []edits.TakeNote{{Note: 60, Velocity: 90, EndTick: 60, Expressions: []edits.TakeExpression{{PitchCents: 50, Timbre: .5}}}}}}}}})
	if r.Code != 422 || !strings.Contains(r.Body.String(), "owning file") || !reflect.DeepEqual(before, noopFiles(t, root)) {
		t.Fatalf("foreign offsets: %d %s", r.Code, r.Body.String())
	}
}
