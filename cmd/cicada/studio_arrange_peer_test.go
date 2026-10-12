package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

const arrangePeerPart = "track bass acid {}\npattern pulse { c3 . c4~ c3 }\n"
const arrangePeerEntry = "cicada 2\narrange {\n  place intro bass pulse {\n    at = @1.2.1\n    length = 1bar\n  }\n  place return bass pulse {\n    at = @3.1.1\n    length = 2bars\n  }\n}\n"

func writeArrangePeerProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"cicada.mod": "project peers\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n", "main.cicada": arrangePeerEntry, "part.cicada": arrangePeerPart} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.cicada")
}

var arrangePeerCases = []struct {
	name     string
	intent   edits.Intent
	from, to string
}{
	{"move", &edits.MoveClip{Entity: "placement:intro", AtTick: 19200}, "at = @1.2.1", "at = @6.1.1"},
	{"resize", &edits.ResizeClip{Entity: "placement:return", LengthTicks: 3840}, "length = 2bars", "length = 1bar"},
	{"split", &edits.SplitClip{Entity: "placement:return", AtTick: 11520}, "    length = 2bars\n  }\n", "    length = 1bar\n  }\n  place return-2 bass pulse {\n    at = @4.1.1\n    length = 1bar\n  }\n"},
	{"duplicate", &edits.DuplicateClip{Entity: "placement:return"}, "    length = 2bars\n  }\n", "    length = 2bars\n  }\n  place return-2 bass pulse {\n    at = @5.1.1\n    length = 2bars\n  }\n"},
	{"delete", &edits.DeleteClip{Entity: "placement:intro"}, "  place intro bass pulse {\n    at = @1.2.1\n    length = 1bar\n  }\n", ""},
}

func TestArrangeIntentsWithPeerFileDefinitionsThroughStudioAndApply(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, c := range arrangePeerCases {
		want := strings.Replace(arrangePeerEntry, c.from, c.to, 1)
		env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(arrangePeerEntry)), Author: "tester", Intents: []edits.Intent{c.intent}}
		t.Run("studio/"+c.name, func(t *testing.T) {
			path := writeArrangePeerProject(t)
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.shutdown() })
			if r := studioCall(t, s.domainRoutes(), "/api/intents", env); r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			if got, _ := os.ReadFile(path); string(got) != want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
			if part, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "part.cicada")); string(part) != arrangePeerPart {
				t.Fatal("peer file changed")
			}
		})
		t.Run("apply/"+c.name, func(t *testing.T) {
			path := writeArrangePeerProject(t)
			file := filepath.Join(filepath.Dir(path), "intents.json")
			writeApplyEnvelope(t, file, edits.Envelope{Version: 1, Intents: []edits.Intent{c.intent}})
			if err := applyCommandWithHooks([]string{path, file, "--write"}, io.Discard, nil); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); string(got) != want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
