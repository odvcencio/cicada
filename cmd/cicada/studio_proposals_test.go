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

func proposalFixture(t *testing.T, source string) (*studio, http.Handler, string) {
	t.Helper()
	path := studioTestPath(t, source)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return s, s.domainRoutes(), path
}

func stageProposal(t *testing.T, handler http.Handler, source, label string, intents ...edits.Intent) string {
	t.Helper()
	proposal := edits.Proposal{ID: "client-id", Label: label, Envelope: edits.Envelope{Version: 1, Revision: studioRevision([]byte(source)), Author: "tester", Session: "proposal-session", Intents: intents}}
	r := studioCall(t, handler, "/api/proposals", proposal)
	var reply struct {
		ID   string            `json:"id"`
		Diff string            `json:"diff"`
		Ops  []edits.PreviewOp `json:"ops"`
	}
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.ID == "" || reply.ID == proposal.ID {
		t.Fatalf("stage: %d %s", r.Code, r.Body.String())
	}
	if len(intents) > 0 && reply.Diff == "" {
		// No-op proposals are checked by their own test below.
		if in, ok := intents[0].(*edits.ReplaceText); !ok || in.Source != source {
			t.Fatal("changed proposal has no diff")
		}
	}
	return reply.ID
}

func deleteProposal(t *testing.T, handler http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, "/api/proposals/"+id, nil)
	request.Host = "127.0.0.1:1234"
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, request)
	return reply
}

func TestStudioProposalAcceptWritesOneHistoryAndAttributedLog(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(map[string]string{"\n": "lf", "\r\n": "crlf"}[newline], func(t *testing.T) {
			source := strings.ReplaceAll(studioMixerScore, "\n", newline)
			s, handler, path := proposalFixture(t, source)
			before := noopFiles(t, filepath.Dir(path))
			intents := []edits.Intent{&edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)}, &edits.SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`0.25`)}}
			id := stageProposal(t, handler, source, "Bass variation", intents...)
			if !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) || len(studioHistoryEdits(t, handler)) != 0 || len(s.proposals) != 1 {
				t.Fatal("staging wrote files or history")
			}
			wantOps := []edits.PreviewOp{{Kind: "setparam", Track: 0, Param: "level", Value: -3}, {Kind: "setparam", Track: 0, Param: "pan", Value: .25}}
			if !reflect.DeepEqual(s.transport.previewOps, wantOps) {
				t.Fatalf("preview ops: %+v", s.transport.previewOps)
			}
			r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
			if r.Code != http.StatusOK {
				t.Fatalf("accept: %d %s", r.Code, r.Body.String())
			}
			changed, _ := os.ReadFile(path)
			history := studioHistoryEdits(t, handler)
			records, skipped, err := editlog.ReadLog(editlog.Path(path))
			if err != nil || skipped != 0 || len(history) != 1 || history[0].Label != "Bass variation" || len(records) != 1 || records[0].Label != "Bass variation" || records[0].Author != "tester" || records[0].Session != "proposal-session" || records[0].Parent != studioRevision([]byte(source)) || records[0].Revision != studioRevision(changed) || len(records[0].Intents) != len(intents) {
				t.Fatalf("history=%+v, records=%+v, skipped=%d, err=%v", history, records, skipped, err)
			}
			wantRaw, _ := (edits.Envelope{Intents: intents}).RawIntents()
			if !reflect.DeepEqual(records[0].Intents, wantRaw) || len(s.proposals) != 0 || len(s.transport.previewOps) != 0 {
				t.Fatal("accept lost intents or retained its preview")
			}
			if r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{}); r.Code != http.StatusNotFound {
				t.Fatalf("repeated accept: %d %s", r.Code, r.Body.String())
			}
			if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)}); r.Code != http.StatusOK {
				t.Fatalf("undo: %d %s", r.Code, r.Body.String())
			}
			if restored, _ := os.ReadFile(path); string(restored) != source {
				t.Fatal("one undo did not restore exact source")
			}
		})
	}
}

func TestStudioProposalConflictRetainsStageAndCanonicalSource(t *testing.T) {
	s, handler, path := proposalFixture(t, studioMixerScore)
	id := stageProposal(t, handler, studioMixerScore, "Variation", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)})
	external := []byte(studioMixerScore + "// external edit\n")
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	before := noopFiles(t, filepath.Dir(path))
	r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{"revision": studioRevision(external)})
	var reply struct{ Revision, Source string }
	if r.Code != http.StatusConflict || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.Revision != studioRevision(external) || reply.Source != string(external) || len(s.proposals) != 1 || len(s.transport.previewOps) != 1 || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) {
		t.Fatalf("conflict: %d %s", r.Code, r.Body.String())
	}
	if r := deleteProposal(t, handler, id); r.Code != http.StatusOK || len(s.proposals) != 0 || len(s.transport.previewOps) != 0 {
		t.Fatalf("discard conflicted proposal: %d %s", r.Code, r.Body.String())
	}
}

func TestStudioProposalDiscardKeepsSourceAndOtherPreview(t *testing.T) {
	s, handler, path := proposalFixture(t, studioMixerScore)
	before := noopFiles(t, filepath.Dir(path))
	first := stageProposal(t, handler, studioMixerScore, "First", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)})
	second := stageProposal(t, handler, studioMixerScore, "Second", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-2`)})
	if first == second {
		t.Fatal("proposal IDs collided")
	}
	if r := deleteProposal(t, handler, first); r.Code != http.StatusOK || len(s.proposals) != 1 || s.transport.previewOps[0].Value != -2 {
		t.Fatalf("inactive discard: %d %s", r.Code, r.Body.String())
	}
	if r := deleteProposal(t, handler, second); r.Code != http.StatusOK || len(s.proposals) != 0 || len(s.transport.previewOps) != 0 {
		t.Fatalf("active discard: %d %s", r.Code, r.Body.String())
	}
	if !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("discard wrote files or history")
	}
	if r := deleteProposal(t, handler, second); r.Code != http.StatusNotFound {
		t.Fatalf("repeated discard: %d %s", r.Code, r.Body.String())
	}
}

func TestStudioProposalNoopAcceptanceDoesNotWrite(t *testing.T) {
	s, handler, path := proposalFixture(t, studioMixerScore)
	before := noopFiles(t, filepath.Dir(path))
	id := stageProposal(t, handler, studioMixerScore, "Same source", &edits.ReplaceText{Source: studioMixerScore})
	r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
	var reply map[string]any
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply["unchanged"] != true || reply["source"] != studioMixerScore || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) || len(studioHistoryEdits(t, handler)) != 0 || len(s.proposals) != 0 {
		t.Fatalf("unchanged accept: %d %s", r.Code, r.Body.String())
	}
}

func TestStudioProposalStageRejectsInvalidRequestsWithoutEffects(t *testing.T) {
	s, handler, path := proposalFixture(t, studioMixerScore)
	before := noopFiles(t, filepath.Dir(path))
	for _, c := range []struct {
		body string
		code int
	}{
		{`{"envelope":{"version":2,"intents":[]}}`, 400},
		{`{"envelope":{"version":1,"revision":"stale","intents":[]}}`, 409},
		{`{"envelope":{"version":1,"intents":[]}}`, 400},
		{`{"envelope":{"version":1,"revision":"` + studioRevision([]byte(studioMixerScore)) + `","intents":[{"kind":"replacetext","source":"???"}]}}`, 422},
		{`{"envelope":{"version":1,"revision":"` + studioRevision([]byte(studioMixerScore)) + `","intents":[{"kind":"setparam","entity":"param:missing.level","value":-3}]}}`, 422},
		{`{"envelope":{"version":1,"intents":[]}} {}`, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/proposals", bytes.NewBufferString(c.body))
		req.Host = "127.0.0.1:1234"
		req.Header.Set("Content-Type", "application/json")
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		if r.Code != c.code {
			t.Fatalf("%s: %d %s", c.body, r.Code, r.Body.String())
		}
	}
	if len(s.proposals) != 0 || len(s.transport.previewOps) != 0 || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("invalid staging had effects")
	}
}

func TestStudioProposalRoutesRejectCrossOriginRequests(t *testing.T) {
	s, handler, path := proposalFixture(t, studioMixerScore)
	id := stageProposal(t, handler, studioMixerScore, "Variation", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)})
	before := noopFiles(t, filepath.Dir(path))
	for _, c := range []struct{ method, route string }{{http.MethodPost, "/api/proposals"}, {http.MethodPost, "/api/proposals/" + id + "/accept"}, {http.MethodDelete, "/api/proposals/" + id}} {
		req := httptest.NewRequest(c.method, c.route, bytes.NewBufferString(`{}`))
		req.Host = "127.0.0.1:1234"
		req.Header.Set("Origin", "https://outside.example")
		req.Header.Set("Content-Type", "application/json")
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		if r.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", c.route, r.Code, r.Body.String())
		}
	}
	if len(s.proposals) != 1 || len(s.transport.previewOps) != 1 || !reflect.DeepEqual(before, noopFiles(t, filepath.Dir(path))) {
		t.Fatal("cross-origin request changed the proposal or files")
	}
}

func TestStudioProposalAcceptRechecksRevisionDuringValidation(t *testing.T) {
	s, handler, path := proposalFixture(t, studioMixerScore)
	id := stageProposal(t, handler, studioMixerScore, "Variation", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)})
	external := []byte(studioMixerScore + "// changed during validation\n")
	compile := studioCompile
	t.Cleanup(func() { studioCompile = compile })
	studioCompile = func(path string, source []byte, overrides map[string][]byte) (*project.Project, error) {
		p, err := compile(path, source, overrides)
		if err == nil {
			err = os.WriteFile(path, external, 0o600)
		}
		return p, err
	}
	r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
	var reply struct{ Revision, Source string }
	if r.Code != http.StatusConflict || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.Revision != studioRevision(external) || reply.Source != string(external) || len(s.proposals) != 1 {
		t.Fatalf("validation conflict: %d %s", r.Code, r.Body.String())
	}
	if records, _, _ := editlog.ReadLog(editlog.Path(path)); len(records) != 0 {
		t.Fatal("validation conflict wrote the edit log")
	}
}

func TestStudioProposalEditionUpgradeChecksAndUndoesManifest(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "accept and undo", true: "manifest conflict"}[conflict], func(t *testing.T) {
			dir := t.TempDir()
			path, manifest := filepath.Join(dir, "main.cicada"), filepath.Join(dir, "cicada.mod")
			source := strings.Replace(studioMixerScore, "cicada 2\n", "", 1)
			originalManifest := []byte("project mixer\ncicada 1\n")
			for path, data := range map[string][]byte{path: []byte(source), manifest: originalManifest} {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.shutdown() })
			handler := s.domainRoutes()
			before := noopFiles(t, dir)
			id := stageProposal(t, handler, source, "Upgrade and adjust bass", &edits.SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`), ConfirmUpgrade: true})
			if !reflect.DeepEqual(before, noopFiles(t, dir)) {
				t.Fatal("staged upgrade wrote the manifest")
			}
			if conflict {
				if err := os.WriteFile(manifest, append(bytes.Clone(originalManifest), []byte("# changed elsewhere\n")...), 0o600); err != nil {
					t.Fatal(err)
				}
				before = noopFiles(t, dir)
			}
			r := studioCall(t, handler, "/api/proposals/"+id+"/accept", map[string]any{})
			if conflict {
				if r.Code != http.StatusConflict || len(s.proposals) != 1 || !reflect.DeepEqual(before, noopFiles(t, dir)) || len(studioHistoryEdits(t, handler)) != 0 {
					t.Fatalf("manifest conflict: %d %s", r.Code, r.Body.String())
				}
				return
			}
			if r.Code != http.StatusOK || len(studioHistoryEdits(t, handler)) != 1 {
				t.Fatalf("upgrade accept: %d %s", r.Code, r.Body.String())
			}
			changed, _ := os.ReadFile(path)
			if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)}); r.Code != http.StatusOK {
				t.Fatalf("upgrade undo: %d %s", r.Code, r.Body.String())
			}
			if restored, _ := os.ReadFile(path); string(restored) != source {
				t.Fatal("upgrade undo did not restore source")
			}
			if restored, _ := os.ReadFile(manifest); !bytes.Equal(restored, originalManifest) {
				t.Fatal("upgrade undo did not restore manifest")
			}
		})
	}
}
