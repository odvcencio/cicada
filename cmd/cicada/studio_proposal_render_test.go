package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/rendercheck"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/project"
)

const (
	proposalSongMain  = "scene a { bass = beat }\nscene b { bass = fill }\nsong { a*2 b*2 }\n"
	proposalPlain     = "1 . 5 . 1 . 5 . 1 . 5 . 1 . 5 ."
	proposalChance    = "1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50"
	proposalSplitPart = "track bass acid {}\npattern pulse { c3 . c4~ c3 }\n"
)

func proposalSongPeer(steps string) string {
	return "track bass acid {}\npattern beat acid {\n  " + steps + "\n}\npattern fill acid {\n  " + steps + "\n}\n"
}

type renderProposalProject struct {
	path, peer string
	handler    http.Handler
}

func newRenderProposalProject(t *testing.T, entry, peer string) renderProposalProject {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"cicada.mod": "project peers\ncicada 2\nentry \"main.cicada\"\nsource \"patterns.cicada\"\n", "main.cicada": entry, "patterns.cicada": peer} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(filepath.Join(dir, "main.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return renderProposalProject{path: filepath.Join(dir, "main.cicada"), peer: filepath.Join(dir, "patterns.cicada"), handler: s.domainRoutes()}
}

func (p renderProposalProject) stage(t *testing.T, entry string, intent edits.Intent) string {
	t.Helper()
	proposal := edits.Proposal{Envelope: edits.Envelope{Version: 1, Revision: studioRevision([]byte(entry)), Author: "tester", Intents: []edits.Intent{intent}}}
	r := studioCall(t, p.handler, "/api/proposals", proposal)
	var reply struct{ ID string }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.ID == "" {
		t.Fatalf("stage: %d %s", r.Code, r.Body.String())
	}
	return reply.ID
}

func TestRenderCheckedProposalsAreReverifiedWhenProjectInputsChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cases := []struct {
		name, entry, peer string
		intent            edits.Intent
		changed           string // peer after staging that changes the render
		harmless          string // peer after staging that keeps the candidate and the render
		cause             string
	}{
		{"convert", proposalSongMain, proposalSongPeer(proposalPlain), &edits.ConvertSongToArrange{},
			proposalSongPeer(proposalChance), proposalSongPeer(proposalPlain) + "// unrelated\n", "chance steps"},
		{"split", "cicada 2\narrange {\n  place run bass pulse {\n    at = @1.1.1\n    length = 4bars\n  }\n}\n", proposalSplitPart, &edits.SplitClip{Entity: "placement:run", AtTick: 7680},
			"track bass acid {}\npattern pulse { c3?50 . c4?50 c3?50 }\n", proposalSplitPart + "// unrelated\n", "restarts its chance steps"},
	}
	for _, c := range cases {
		t.Run(c.name+"/unchanged", func(t *testing.T) {
			p := newRenderProposalProject(t, c.entry, c.peer)
			id := p.stage(t, c.entry, c.intent)
			if r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{}); r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			if got, _ := os.ReadFile(p.path); string(got) == c.entry {
				t.Fatal("accepted proposal wrote nothing")
			}
			if len(studioHistoryEdits(t, p.handler)) != 1 {
				t.Fatal("expected one history entry")
			}
		})
		t.Run(c.name+"/peer change that alters the render", func(t *testing.T) {
			p := newRenderProposalProject(t, c.entry, c.peer)
			id := p.stage(t, c.entry, c.intent)
			if err := os.WriteFile(p.peer, []byte(c.changed), 0o600); err != nil {
				t.Fatal(err)
			}
			r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{})
			if r.Code != 422 || !strings.Contains(r.Body.String(), c.cause) {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			if got, _ := os.ReadFile(p.path); string(got) != c.entry {
				t.Fatal("a refused acceptance wrote the entry")
			}
			if len(studioHistoryEdits(t, p.handler)) != 0 {
				t.Fatal("a refused acceptance left a history entry")
			}
			// A fresh request is refused the same way.
			fresh := edits.Envelope{Version: 1, Revision: studioRevision([]byte(c.entry)), Intents: []edits.Intent{c.intent}}
			if r := studioCall(t, p.handler, "/api/intents", fresh); r.Code != 422 {
				t.Fatalf("fresh request: %d %s", r.Code, r.Body.String())
			}
		})
		// Rule: when project inputs change, the envelope is run again. If the
		// result is the same candidate and the render still matches, acceptance
		// proceeds; a different candidate is a conflict that asks to restage.
		t.Run(c.name+"/peer change that does not affect the render", func(t *testing.T) {
			p := newRenderProposalProject(t, c.entry, c.peer)
			id := p.stage(t, c.entry, c.intent)
			if err := os.WriteFile(p.peer, []byte(c.harmless), 0o600); err != nil {
				t.Fatal(err)
			}
			if r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{}); r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			if len(studioHistoryEdits(t, p.handler)) != 1 {
				t.Fatal("expected one history entry")
			}
		})
	}
}

// A peer change after staging that makes the intent invalid (a 3-step pattern
// puts tick 7680 off a pattern boundary) is refused when the envelope runs again.
func TestRenderCheckedProposalIsRefusedWhenTheIntentNoLongerHolds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	entry := "cicada 2\narrange {\n  place run bass pulse {\n    at = @1.1.1\n    length = 4bars\n  }\n}\n"
	p := newRenderProposalProject(t, entry, proposalSplitPart)
	id := p.stage(t, entry, &edits.SplitClip{Entity: "placement:run", AtTick: 7680})
	if err := os.WriteFile(p.peer, []byte("track bass acid {}\npattern pulse { c3 . c4 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{})
	if r.Code != 422 || !strings.Contains(r.Body.String(), "pattern-length boundary") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(p.path); string(got) != entry || len(studioHistoryEdits(t, p.handler)) != 0 {
		t.Fatal("acceptance wrote after the inputs changed")
	}
}

// A change after a comparison took its snapshot cannot alter what that comparison
// rendered. It is caught before anything is written: the commit tail refuses a
// direct request, and a staged proposal is run again on acceptance and refused.
func TestProjectChangeAfterTheSnapshotIsCaughtBeforeAnyWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const cause = "the project changed during the render check; try again"
	for _, route := range []string{"/api/intents", "/api/proposals"} {
		t.Run(route, func(t *testing.T) {
			p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
			t.Cleanup(func() { rendercheck.AfterSnapshot = nil })
			rendercheck.AfterSnapshot = func() {
				_ = os.WriteFile(p.peer, []byte(proposalSongPeer(proposalChance)), 0o600)
			}
			env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(proposalSongMain)), Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
			if route == "/api/intents" {
				r := studioCall(t, p.handler, route, env)
				if r.Code != 409 || !strings.Contains(r.Body.String(), cause) {
					t.Fatalf("%d %s", r.Code, r.Body.String())
				}
			} else {
				id := p.stage(t, proposalSongMain, &edits.ConvertSongToArrange{})
				r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{})
				if r.Code != 422 || !strings.Contains(r.Body.String(), "chance steps") {
					t.Fatalf("accept: %d %s", r.Code, r.Body.String())
				}
			}
			if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain || len(studioHistoryEdits(t, p.handler)) != 0 {
				t.Fatal("a refused request wrote")
			}
		})
	}
}

// The shared comparison the commit tail runs (and cicada apply runs before its own exchange).
func TestRenderIdentityIsRecheckedBeforeTheCommit(t *testing.T) {
	p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
	s, err := newStudio(p.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	opts, err := s.editOptions([]byte(proposalSongMain), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := edits.Apply([]byte(proposalSongMain), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}, opts)
	if err != nil || result.RenderIdentity == "" || !result.RenderChecked {
		t.Fatalf("apply: %v identity=%q", err, result.RenderIdentity)
	}
	if err := s.verifyRenderIdentity(result.RenderChecked, result.RenderIdentity, result.RenderRecheck); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.peer, []byte(proposalSongPeer(proposalChance)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyRenderIdentity(result.RenderChecked, result.RenderIdentity, result.RenderRecheck); err == nil || !strings.Contains(err.Error(), "changed during the render check") {
		t.Fatalf("recheck: %v", err)
	}
}

// V5: the peer changes while acceptance compiles the candidate, after the
// acceptance-time identity comparison. The commit tail must still refuse.
func TestPeerChangeDuringAcceptanceCompileIsRefusedByTheCommitTail(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
	id := p.stage(t, proposalSongMain, &edits.ConvertSongToArrange{})
	previous := studioCompile
	t.Cleanup(func() { studioCompile = previous })
	studioCompile = func(path string, source []byte, overrides map[string][]byte) (*project.Project, error) {
		compiled, err := previous(path, source, overrides)
		_ = os.WriteFile(p.peer, []byte(proposalSongPeer(proposalChance)), 0o600)
		return compiled, err
	}
	r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{})
	studioCompile = previous
	if r.Code != 409 || !strings.Contains(r.Body.String(), "the project changed during the render check; try again") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain || len(studioHistoryEdits(t, p.handler)) != 0 {
		t.Fatal("the commit tail wrote after the project changed")
	}
	// A fresh request sees the changed project and is refused for the real reason.
	fresh := edits.Envelope{Version: 1, Revision: studioRevision([]byte(proposalSongMain)), Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
	if r := studioCall(t, p.handler, "/api/intents", fresh); r.Code != 422 || !strings.Contains(r.Body.String(), "chance steps") {
		t.Fatalf("fresh request: %d %s", r.Code, r.Body.String())
	}
}

// Every mutation that carries a verified identity is checked in the tail, and one
// that was render-checked without an identity is refused.
func TestCommitTailRefusesStaleOrMissingRenderIdentity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
	s, err := newStudio(p.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	opts, err := s.editOptions([]byte(proposalSongMain), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := edits.Apply([]byte(proposalSongMain), edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	commit := func(identity string) commitOutcome {
		s.mu.Lock()
		defer s.mu.Unlock()
		mutation := studioMutation{Source: result.Source, RenderChecked: true, RenderIdentity: identity, RenderRecheck: result.RenderRecheck}
		return s.commitMutationLocked(studioEdit{Revision: studioRevision([]byte(proposalSongMain))}, []byte(proposalSongMain), mutation, nil, studioHistoryWriteNew, 0, commitHook{})
	}
	if outcome := commit(""); outcome.Status != 409 {
		t.Fatalf("missing identity: %d %v", outcome.Status, outcome.Response)
	}
	if outcome := commit("stale"); outcome.Status != 409 {
		t.Fatalf("stale identity: %d %v", outcome.Status, outcome.Response)
	}
	if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain {
		t.Fatal("a refused commit wrote")
	}
	if outcome := commit(result.RenderIdentity); outcome.Status != 200 {
		t.Fatalf("verified identity: %d %v", outcome.Status, outcome.Response)
	}
}

// A peer rewritten during the render and put back before any later check leaves
// the disk identical to the start, so before/after comparisons cannot see it.
// The renders read a snapshot, so what was rendered is what a fresh comparison
// of the original project renders: here the chance steps, which are refused.
func TestPeerRewrittenAndRestoredDuringTheRenderCannotApproveAConversion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, route := range []string{"/api/intents", "/api/proposals"} {
		t.Run(route, func(t *testing.T) {
			p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalChance))
			t.Cleanup(func() { rendercheck.AfterSnapshot, rendercheck.AfterRender = nil, nil })
			rendercheck.AfterSnapshot = func() { _ = os.WriteFile(p.peer, []byte(proposalSongPeer(proposalPlain)), 0o600) }
			rendercheck.AfterRender = func() { _ = os.WriteFile(p.peer, []byte(proposalSongPeer(proposalChance)), 0o600) }
			env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(proposalSongMain)), Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
			var body any = env
			if route == "/api/proposals" {
				body = edits.Proposal{Envelope: env}
			}
			for attempt := 1; attempt <= 2; attempt++ {
				r := studioCall(t, p.handler, route, body)
				if r.Code != 422 || !strings.Contains(r.Body.String(), "chance steps") {
					t.Fatalf("attempt %d: %d %s", attempt, r.Code, r.Body.String())
				}
			}
			if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain || len(studioHistoryEdits(t, p.handler)) != 0 {
				t.Fatal("a refused request wrote")
			}
		})
	}
}

// V6: an earlier intent in the batch (ReplaceText) introduces a sampler. The
// verified identity describes that working score, so removing the sample after
// acceptance compiles is caught by the commit tail.
func TestRenderIdentityCoversDependenciesIntroducedByTheBatch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	original := "cicada 2\ntrack bass acid {}\npattern melody { c3 . c4 . }\nscene a { bass = melody }\nscene b { bass = melody }\nsong { a*2 b*2 }\n"
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	sample := filepath.Join(dir, "wave.wav")
	for name, data := range map[string][]byte{"main.cicada": []byte(original), "wave.wav": wav} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	working := fmt.Sprintf("cicada 2\nasset wave \"wave.wav\" { sha256=\"%x\" format=wav frames=480 rate=48000Hz channels=1 }\nsampler hit { asset=wave root=c3 mode=oneshot voices=8 }\ntrack lead hit {}\npattern melody { c3 . c3 . }\nscene a { lead = melody }\nscene b { lead = melody }\nsong { a*2 b*2 }\n", sha256.Sum256(wav))
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	handler := s.domainRoutes()
	proposal := edits.Proposal{Envelope: edits.Envelope{Version: 1, Revision: studioRevision([]byte(original)), Intents: []edits.Intent{&edits.ReplaceText{Source: working}, &edits.ConvertSongToArrange{}}}}
	r := studioCall(t, handler, "/api/proposals", proposal)
	var staged struct{ ID string }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &staged) != nil || staged.ID == "" {
		t.Fatalf("stage: %d %s", r.Code, r.Body.String())
	}
	previous := studioCompile
	t.Cleanup(func() { studioCompile = previous })
	studioCompile = func(path string, source []byte, overrides map[string][]byte) (*project.Project, error) {
		compiled, err := previous(path, source, overrides)
		_ = os.Remove(sample)
		return compiled, err
	}
	r = studioCall(t, handler, "/api/proposals/"+staged.ID+"/accept", map[string]any{})
	studioCompile = previous
	if r.Code != 409 || !strings.Contains(r.Body.String(), "the project changed during the render check; try again") {
		t.Fatalf("accept: %d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != original || len(studioHistoryEdits(t, handler)) != 0 {
		t.Fatal("the commit tail wrote a score that cannot render")
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// The approval can expire while the replacement file is being staged. A peer
// change at the publication boundary, after the temporary file is written and
// just before the exchange, must abort the write and leave nothing behind.
func TestPublicationBoundaryVetoesAStaleRenderApproval(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const cause = "the project changed during the render check; try again"
	boundary := func(t *testing.T, peer string) {
		t.Cleanup(func() { studioPublishSeam = nil })
		studioPublishSeam = func() { _ = os.WriteFile(peer, []byte(proposalSongPeer(proposalChance)), 0o600) }
	}
	t.Run("direct", func(t *testing.T) {
		p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
		boundary(t, p.peer)
		names := dirNames(t, filepath.Dir(p.path))
		env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(proposalSongMain)), Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
		if r := studioCall(t, p.handler, "/api/intents", env); r.Code != 409 || !strings.Contains(r.Body.String(), cause) {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
		if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain || len(studioHistoryEdits(t, p.handler)) != 0 {
			t.Fatal("the vetoed write left a trace")
		}
		if after := dirNames(t, filepath.Dir(p.path)); strings.Join(after, ",") != strings.Join(names, ",") {
			t.Fatalf("staged files left behind: %v -> %v", names, after)
		}
	})
	t.Run("proposal acceptance", func(t *testing.T) {
		p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
		id := p.stage(t, proposalSongMain, &edits.ConvertSongToArrange{})
		boundary(t, p.peer)
		names := dirNames(t, filepath.Dir(p.path))
		if r := studioCall(t, p.handler, "/api/proposals/"+id+"/accept", map[string]any{}); r.Code != 409 || !strings.Contains(r.Body.String(), cause) {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
		if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain || len(studioHistoryEdits(t, p.handler)) != 0 {
			t.Fatal("the vetoed write left a trace")
		}
		if after := dirNames(t, filepath.Dir(p.path)); strings.Join(after, ",") != strings.Join(names, ",") {
			t.Fatalf("staged files left behind: %v -> %v", names, after)
		}
	})
	t.Run("cicada apply", func(t *testing.T) {
		p := newRenderProposalProject(t, proposalSongMain, proposalSongPeer(proposalPlain))
		file := filepath.Join(t.TempDir(), "intents.json")
		writeApplyEnvelope(t, file, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.ConvertSongToArrange{}}})
		boundary(t, p.peer)
		names := dirNames(t, filepath.Dir(p.path))
		err := applyCommandWithHooks([]string{p.path, file, "--write"}, io.Discard, nil)
		if !errors.Is(err, edits.ErrProjectChanged) {
			t.Fatalf("got %v", err)
		}
		if got, _ := os.ReadFile(p.path); string(got) != proposalSongMain {
			t.Fatal("the vetoed write changed the score")
		}
		if after := dirNames(t, filepath.Dir(p.path)); strings.Join(after, ",") != strings.Join(names, ",") {
			t.Fatalf("staged files left behind: %v -> %v", names, after)
		}
	})
}

// A manifest discovered on disk after the renders changes the resolved project.
// The identity includes the resolution, so the commit tail refuses.
func TestNewlyDiscoveredManifestIsRefusedByTheCommitTail(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	loose := "title \"t\"\ntempo 130\ntrack bass acid {}\npattern hits { c3 . c4 . }\nscene a { bass = hits }\nscene b { bass = hits }\nsong { a*2 b*2 }\n"
	if err := os.WriteFile(path, []byte(loose), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	manifest := filepath.Join(dir, "cicada.mod")
	t.Cleanup(func() { rendercheck.AfterSnapshot = nil })
	rendercheck.AfterSnapshot = func() { _ = os.WriteFile(manifest, []byte("project late\ncicada 2\n"), 0o600) }
	env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(loose)), Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
	r := studioCall(t, s.domainRoutes(), "/api/intents", env)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "the project changed during the render check; try again") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != loose {
		t.Fatal("a refused request wrote")
	}
}
