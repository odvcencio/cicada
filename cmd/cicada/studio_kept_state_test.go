package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/benchscore/applybench"
	"m31labs.dev/cicada/project"
)

// requireKeptIsDisk checks that the studio's kept state holds exactly the
// score on disk and equals a full compile of it (section B.6, items 2 to 4).
func requireKeptIsDisk(t *testing.T, s *studio) {
	t.Helper()
	disk, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	requireStateIsAFullCompile(t, s.path, s.kept, disk)
}

func requireStateIsAFullCompile(t *testing.T, path string, state *project.State, source []byte) {
	t.Helper()
	if state == nil || !bytes.Equal(state.Source(), source) || len(state.Files()) != 0 {
		t.Fatal("the kept state does not hold the score's bytes")
	}
	produced, err := applybench.StateOf(state)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := applybench.FullCompile(path, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applybench.Compare(produced, fresh); err != nil {
		t.Fatalf("the kept state is not a full compile of its bytes: %v", err)
	}
}

// keptStateEdit sends one intent through /api/intents with the score's
// current revisions, as Studio does.
func keptStateEdit(t *testing.T, handler http.Handler, path string, intent edits.Intent) (int, string) {
	t.Helper()
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := project.ReadScoreRevision(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(edits.Envelope{Version: edits.EnvelopeVersion, Revision: studioRevision(disk), Intents: []edits.Intent{intent}})
	if err != nil {
		t.Fatal(err)
	}
	r := studioCallRaw(t, handler, "/api/intents", string(body), receipt)
	return r.Code, r.Body.String()
}

// The edit service keeps one compiled state between a workspace read and the
// next edit and between edits, and only for the exact bytes on disk: a
// change saved by another program between two edits is compiled afresh, and
// the second edit gives what the service gives without a kept state.
func TestStudioKeptStateFollowsAnOutsideChange(t *testing.T) {
	isolateState(t)
	s, handler, path := proposalFixture(t, studioScore)
	if r := studioCall(t, handler, "/api/workspace", nil); r.Code != http.StatusOK {
		t.Fatalf("workspace: %d %s", r.Code, r.Body)
	}
	read := s.kept
	requireKeptIsDisk(t, s)
	if code, body := keptStateEdit(t, handler, path, &edits.ToggleStep{Entity: "step:pulse/1", Shared: "definition"}); code != http.StatusOK {
		t.Fatalf("first edit: %d %s", code, body)
	}
	if s.kept == read {
		t.Fatal("the studio kept the state of the bytes before the edit")
	}
	requireKeptIsDisk(t, s)
	committed := s.kept

	disk, _ := os.ReadFile(path)
	outside := bytes.Replace(disk, []byte("title \"Studio\""), []byte("title \"Saved elsewhere\""), 1)
	if bytes.Equal(outside, disk) {
		t.Fatal("the outside change changed nothing")
	}
	if err := os.WriteFile(path, outside, 0o600); err != nil {
		t.Fatal(err)
	}
	intent := &edits.ToggleStep{Entity: "step:pulse/2", Shared: "definition"}
	opts, err := (&studio{path: path}).editOptions(outside, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := edits.Apply(outside, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := keptStateEdit(t, handler, path, intent); code != http.StatusOK {
		t.Fatalf("edit after the outside change: %d %s", code, body)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want.Source) {
		t.Fatalf("after an outside change the edit wrote\n%s\nwant\n%s", got, want.Source)
	}
	if s.kept == committed {
		t.Fatal("the studio kept a state of bytes no longer on disk")
	}
	requireKeptIsDisk(t, s)
	// The state the first edit committed still holds its own bytes.
	requireStateIsAFullCompile(t, path, committed, disk)
}

// A refused edit leaves the kept state as it was before the edit (B.6 item
// 5), and the next edit is accepted from it.
func TestStudioRefusedEditKeepsTheState(t *testing.T) {
	isolateState(t)
	s, handler, path := proposalFixture(t, studioScore)
	if r := studioCall(t, handler, "/api/workspace", nil); r.Code != http.StatusOK {
		t.Fatalf("workspace: %d %s", r.Code, r.Body)
	}
	before := s.kept
	for name, intent := range map[string]edits.Intent{
		"unknown pattern":   &edits.ToggleStep{Entity: "step:missing/0", Shared: "definition"},
		"invalid candidate": &edits.ReplaceText{Source: "???"},
	} {
		if code, body := keptStateEdit(t, handler, path, intent); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s, want a refusal", name, code, body)
		}
		if s.kept != before {
			t.Fatalf("%s: the refusal replaced the kept state", name)
		}
		requireKeptIsDisk(t, s)
	}
	if code, body := keptStateEdit(t, handler, path, &edits.SetPitch{Entity: "step:pulse/0", Pitch: 50, Shared: "definition"}); code != http.StatusOK {
		t.Fatalf("edit after refusals: %d %s", code, body)
	}
	requireKeptIsDisk(t, s)
	requireStateIsAFullCompile(t, path, before, []byte(studioScore))
}

// cicada apply runs a batch of 50 intents on one chain of compiled states and
// writes what applying them one at a time writes. The time is logged; the
// speed criterion is measured by the edit benchmark, not asserted here.
func TestApplyBatchOfFiftyIntents(t *testing.T) {
	isolateState(t)
	dir := t.TempDir()
	score, err := os.ReadFile(filepath.Join("..", "..", "examples", "songs", "anywhere-you-are.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "examples", "songs", "cicada.mod"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "anywhere-you-are.cicada")
	if err := os.WriteFile(path, score, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cicada.mod"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, _, err := project.LoadScore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	var intents []edits.Intent
	for _, pattern := range parsed.Patterns {
		for _, lane := range pattern.Lanes {
			for step := range lane.Hits {
				if len(intents) < 50 {
					intents = append(intents, &edits.ToggleStep{Entity: edits.EntityID(fmt.Sprintf("step:%s/%s/%d", pattern.Name, lane.Name, step)), Shared: "definition"})
				}
			}
		}
	}
	if len(intents) != 50 {
		t.Fatalf("%d drum steps, want 50", len(intents))
	}
	want := score
	for _, intent := range intents {
		opts, err := (&studio{path: path}).editOptions(want, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := edits.Apply(want, edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}, opts)
		if err != nil {
			t.Fatalf("%s alone: %v", intent.(*edits.ToggleStep).Entity, err)
		}
		want = result.Source
	}
	data, err := json.Marshal(edits.Envelope{Version: edits.EnvelopeVersion, Revision: edits.Revision(score), Intents: intents})
	if err != nil {
		t.Fatal(err)
	}
	intentsPath := filepath.Join(dir, "intents.json")
	if err := os.WriteFile(intentsPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	compiles := 0
	previous := studioCompile
	t.Cleanup(func() { studioCompile = previous })
	studioCompile = func(state *project.State) (*project.Project, error) {
		compiles++
		return previous(state)
	}
	var out bytes.Buffer
	start := time.Now()
	if err := applyCommandWithHooks([]string{path, intentsPath, "--write", "--json"}, &out, nil); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	t.Logf("50 togglestep intents in one cicada apply: %v (whole command, unmeasured host)", time.Since(start))
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatal("the batch wrote other bytes than the intents one at a time")
	}
	if compiles != 1 {
		t.Fatalf("the batch compiled %d times, want once (its result)", compiles)
	}
}
