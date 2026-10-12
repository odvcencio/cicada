package edit_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

// The track and pattern live in a peer file; the entry holds only the arrange
// block. A standalone parse of the entry cannot resolve them, so these edits
// must be validated by the project-aware compile.
const peerPart = "track bass acid {}\npattern pulse { c3 . c4~ c3 }\n"
const peerEntry = "cicada 2\narrange {\n  place intro bass pulse {\n    at = @1.2.1\n    length = 1bar\n  }\n  place return bass pulse {\n    at = @3.1.1\n    length = 2bars\n  }\n}\n"

func applyPeer(t *testing.T, raw string) (*edits.Result, error) {
	t.Helper()
	files := []notation.SourceFile{{Path: "main.cicada", Source: []byte(peerEntry)}, {Path: "part.cicada", Source: []byte(peerPart)}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	if _, err := compiler.Compile([]byte(peerEntry), nil); err != nil {
		t.Fatalf("fixture does not compile: %v", err)
	}
	var env edits.Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+raw+`]}`), &env); err != nil {
		t.Fatal(err)
	}
	return edits.Apply([]byte(peerEntry), env, edits.Options{Compiler: compiler, Path: "main.cicada", Edition: 2, Sources: files, ParseProject: compiler.parse})
}

func TestArrangeIntentsWorkWithCompanionDefinitionsInPeerFile(t *testing.T) {
	for _, c := range []struct{ raw, from, to string }{
		{`{"kind":"moveclip","entity":"placement:intro","attick":19200}`, "at = @1.2.1", "at = @6.1.1"},
		{`{"kind":"resizeclip","entity":"placement:return","lengthticks":3840}`, "length = 2bars", "length = 1bar"},
		{`{"kind":"splitclip","entity":"placement:return","attick":11520}`, "    length = 2bars\n  }\n", "    length = 1bar\n  }\n  place return-2 bass pulse {\n    at = @4.1.1\n    length = 1bar\n  }\n"},
		{`{"kind":"duplicateclip","entity":"placement:return"}`, "    length = 2bars\n  }\n", "    length = 2bars\n  }\n  place return-2 bass pulse {\n    at = @5.1.1\n    length = 2bars\n  }\n"},
		{`{"kind":"deleteclip","entity":"placement:intro"}`, "  place intro bass pulse {\n    at = @1.2.1\n    length = 1bar\n  }\n", ""},
	} {
		t.Run(c.raw, func(t *testing.T) {
			got, err := applyPeer(t, c.raw)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Replace(peerEntry, c.from, c.to, 1); !bytes.Equal(got.Source, []byte(want)) || len(got.Files) != 0 {
				t.Fatalf("got:\n%s\nwant:\n%s", got.Source, want)
			}
		})
	}
}

type sameRender struct{}

func (sameRender) Hash([]byte) (string, error) { return "same", nil }

func TestConvertSongWithCompanionDefinitionsInPeerFile(t *testing.T) {
	entry := "cicada 2\nscene a { bass = pulse }\nscene b { bass = pulse }\nsong { a*2 b*1 }\n"
	files := []notation.SourceFile{{Path: "main.cicada", Source: []byte(entry)}, {Path: "part.cicada", Source: []byte(peerPart)}}
	compiler := multiFileCompiler{path: "main.cicada", files: files}
	var env edits.Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[{"kind":"convertsongtoarrange"}]}`), &env); err != nil {
		t.Fatal(err)
	}
	got, err := edits.Apply([]byte(entry), env, edits.Options{Compiler: compiler, Path: "main.cicada", Edition: 2, Sources: files, ParseProject: compiler.parse, RenderCheck: sameRender{}})
	if err != nil {
		t.Fatal(err)
	}
	want := "cicada 2\nscene a { bass = pulse }\nscene b { bass = pulse }\narrange {\n  place a-1-bass bass pulse {\n    at = @1.1.1 length = 3bars\n  }\n}\n"
	if string(got.Source) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got.Source, want)
	}
}
