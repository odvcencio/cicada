package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
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

// changeAfterRenders wraps the Studio render check and runs mutate once the
// second render of a comparison has returned.
type changeAfterRenders struct {
	edits.IdentifiedRenderCheck
	calls  int
	mutate func()
}

func (c *changeAfterRenders) HashWith(source []byte, files map[string][]byte) (string, error) {
	hash, err := c.IdentifiedRenderCheck.HashWith(source, files)
	if c.calls++; c.calls == 2 {
		c.mutate()
	}
	return hash, err
}

func (c *changeAfterRenders) Hash(source []byte) (string, error) { return c.HashWith(source, nil) }

// A peer edit that lands right after the second render must not be recorded as
// verified: direct intents and staged proposals both refuse.
func TestProjectChangeBetweenRendersIsRefusedByIntentsAndStaging(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const cause = "the project changed during the render check; try again"
	for _, route := range []string{"/api/intents", "/api/proposals"} {
		t.Run(route, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"cicada.mod": "project peers\ncicada 2\nentry \"main.cicada\"\nsource \"patterns.cicada\"\n", "main.cicada": proposalSongMain, "patterns.cicada": proposalSongPeer(proposalPlain)} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path, peer := filepath.Join(dir, "main.cicada"), filepath.Join(dir, "patterns.cicada")
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.shutdown() })
			s.renderCheck = &changeAfterRenders{IdentifiedRenderCheck: s.renderCheck.(edits.IdentifiedRenderCheck), mutate: func() {
				_ = os.WriteFile(peer, []byte(proposalSongPeer(proposalChance)), 0o600)
			}}
			env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(proposalSongMain)), Intents: []edits.Intent{&edits.ConvertSongToArrange{}}}
			var body any = env
			if route == "/api/proposals" {
				body = edits.Proposal{Envelope: env}
			}
			r := studioCall(t, s.domainRoutes(), route, body)
			if r.Code != 422 || !strings.Contains(r.Body.String(), cause) {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			if got, _ := os.ReadFile(path); string(got) != proposalSongMain || len(studioHistoryEdits(t, s.domainRoutes())) != 0 {
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
	if err := s.verifyRenderIdentity(result.RenderChecked, result.RenderIdentity, []byte(proposalSongMain)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.peer, []byte(proposalSongPeer(proposalChance)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyRenderIdentity(result.RenderChecked, result.RenderIdentity, []byte(proposalSongMain)); err == nil || !strings.Contains(err.Error(), "changed during the render check") {
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
		mutation := studioMutation{Source: result.Source, RenderChecked: true, RenderIdentity: identity}
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
