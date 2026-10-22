package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func secondaryArrangementStudio(t *testing.T) (*studio, []byte, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	cut := strings.Index(studioPlacementScore, "arrange {")
	entry := []byte(studioPlacementScore[:cut])
	part := []byte(strings.ReplaceAll(studioPlacementScore[cut:], "\n", "\r\n"))
	for name, data := range map[string][]byte{"main.cicada": entry, "arrangement.cicada": part, "cicada.mod": []byte("project arrangement-sources\ncicada 2\nentry \"main.cicada\"\nsource \"arrangement.cicada\"\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.shutdown() })
	return s, entry, part, filepath.Join(dir, "arrangement.cicada")
}

func arrangementSourceActions() []studioEdit {
	return []studioEdit{
		{Action: "update", Name: "one", Placement: &studioPlaceSettings{At: "@2.1.1", Length: "3bars"}},
		{Action: "append", NewName: "ending", Placement: &studioPlaceSettings{At: "@9.1.1", Length: "1bar", Track: "bass", Content: "pulse"}},
		{Action: "duplicate", Name: "one", NewName: "copy", Placement: &studioPlaceSettings{At: "@9.1.1"}},
		{Action: "delete", Name: "one"},
		{Action: "marker", Name: "return", Placement: &studioPlaceSettings{At: "@6.1.1"}},
		{Action: "append-marker", NewName: "finish", Placement: &studioPlaceSettings{At: "@9.1.1"}},
		{Action: "delete-marker", Name: "return"},
	}
}

func TestSecondaryArrangementActionsUndoRedoAndReopen(t *testing.T) {
	isolateState(t)
	for _, request := range arrangementSourceActions() {
		t.Run(request.Action, func(t *testing.T) {
			s, entry, part, partPath := secondaryArrangementStudio(t)
			seen, err := project.ReadScoreRevision(s.path)
			if err != nil {
				t.Fatal(err)
			}
			request.Revision, request.SourceRevision = studioRevision(entry), seen
			r := studioCall(t, s.routes(), "/api/arrange", request)
			if r.Code != http.StatusOK {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			after, err := os.ReadFile(partPath)
			if err != nil || bytes.Equal(after, part) {
				t.Fatal("arrangement edit did not reach its source", err)
			}
			if got, _ := os.ReadFile(s.path); !bytes.Equal(got, entry) {
				t.Fatal("entry changed")
			}
			if strings.Contains(strings.ReplaceAll(string(after), "\r\n", ""), "\n") {
				t.Fatal("line endings changed")
			}
			for _, step := range []struct {
				route string
				want  []byte
			}{{"/api/undo", part}, {"/api/redo", after}} {
				r = studioCall(t, s.routes(), step.route, studioEdit{Revision: studioRevision(entry)})
				if r.Code != 200 {
					t.Fatalf("%s: %d %s", step.route, r.Code, r.Body.String())
				}
				if got, _ := os.ReadFile(partPath); !bytes.Equal(got, step.want) {
					t.Fatal(step.route, "did not restore exact arrangement bytes")
				}
			}
			if err := s.shutdown(); err != nil {
				t.Fatal(err)
			}
			reopened, err := newStudio(s.path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.shutdown()
			if got, _ := os.ReadFile(partPath); !bytes.Equal(got, after) {
				t.Fatal("restart changed arrangement")
			}
			if _, err := compileStudioSource(reopened.path, entry); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSecondaryArrangementRejectsUnseenAndMidCommitWrites(t *testing.T) {
	isolateState(t)
	for _, request := range arrangementSourceActions() {
		for _, mid := range []bool{false, true} {
			t.Run(request.Action+map[bool]string{false: "/unseen", true: "/mid-commit"}[mid], func(t *testing.T) {
				s, entry, part, partPath := secondaryArrangementStudio(t)
				seen, err := project.ReadScoreRevision(s.path)
				if err != nil {
					t.Fatal(err)
				}
				request.Revision, request.SourceRevision = studioRevision(entry), seen
				external := append(bytes.Clone(part), []byte("// another editor saved this\r\n")...)
				write := func() {
					if err := os.WriteFile(partPath, external, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mid {
					s.afterStage = write
				} else {
					write()
				}
				r := studioCall(t, s.routes(), "/api/arrange", request)
				if r.Code != http.StatusConflict {
					t.Fatalf("%d %s", r.Code, r.Body.String())
				}
				if got, _ := os.ReadFile(partPath); !bytes.Equal(got, external) {
					t.Fatal("external arrangement changed")
				}
				if got, _ := os.ReadFile(s.path); !bytes.Equal(got, entry) {
					t.Fatal("entry changed")
				}
				if len(s.history.edits) != 0 {
					t.Fatal("refused edit entered history")
				}
			})
		}
	}
}

func TestSecondaryArrangementLegacyIntentsAndApply(t *testing.T) {
	isolateState(t)
	for _, c := range arrangePeerCases {
		for _, apply := range []bool{false, true} {
			t.Run(c.name+map[bool]string{false: "/studio", true: "/apply"}[apply], func(t *testing.T) {
				path := writeArrangePeerProject(t)
				partPath := filepath.Join(filepath.Dir(path), "part.cicada")
				entry := []byte("cicada 2\n" + arrangePeerPart)
				part := []byte(strings.TrimPrefix(arrangePeerEntry, "cicada 2\n"))
				if err := os.WriteFile(path, entry, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(partPath, part, 0600); err != nil {
					t.Fatal(err)
				}
				env := edits.Envelope{Version: 1, Revision: studioRevision(entry), Intents: []edits.Intent{c.intent}}
				if apply {
					file := filepath.Join(filepath.Dir(path), "intents.json")
					writeApplyEnvelope(t, file, env)
					if err := applyCommandWithHooks([]string{path, file, "--write"}, io.Discard, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					s, err := newStudio(path)
					if err != nil {
						t.Fatal(err)
					}
					defer s.shutdown()
					if r := studioCall(t, s.domainRoutes(), "/api/intents", env); r.Code != 200 {
						t.Fatalf("%d %s", r.Code, r.Body.String())
					}
				}
				if got, _ := os.ReadFile(path); !bytes.Equal(got, entry) {
					t.Fatal("legacy edit changed entry")
				}
				want := strings.Replace(string(part), c.from, c.to, 1)
				if got, _ := os.ReadFile(partPath); string(got) != want {
					t.Fatalf("wrong owning source: %s", got)
				}
			})
		}
	}
}
