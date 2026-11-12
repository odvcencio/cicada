package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// numbered returns n lines "line 1" to "line n", each ending in newline.
func numbered(n int, newline string) []byte {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d%s", i, newline)
	}
	return []byte(b.String())
}

// replaceLine returns text with line n (from 1) replaced by with.
func replaceLine(text []byte, n int, with string) []byte {
	lines := bytes.SplitAfter(text, []byte("\n"))
	lines[n-1] = []byte(with)
	return bytes.Join(lines, nil)
}

// TestDiffThenPatchGivesTheTarget checks apply(from, diff(from, to)) = to,
// for line endings LF and CRLF, texts with and without a final newline, empty
// texts, a text too large for the line table, and random edits.
func TestDiffThenPatchGivesTheTarget(t *testing.T) {
	type pair struct{ name, from, to string }
	pairs := []pair{
		{"same", "a\nb\n", "a\nb\n"},
		{"from empty", "", "a\nb\n"},
		{"to empty", "a\nb\n", ""},
		{"one line", "a\n", "b\n"},
		{"no final newline", "a\nb", "a\nc"},
		{"adds final newline", "a\nb", "a\nb\n"},
		{"drops final newline", "a\nb\n", "a\nb"},
		{"append", "a\nb\n", "a\nb\nc\n"},
		{"prepend", "a\nb\n", "z\na\nb\n"},
		{"crlf", "a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n"},
		{"crlf to lf", "a\r\nb\r\n", "a\nb\n"},
		{"two hunks", string(numbered(40, "\n")), string(replaceLine(replaceLine(numbered(40, "\n"), 3, "three\n"), 35, "thirty-five\n"))},
	}
	big := numbered(1500, "\n")
	pairs = append(pairs, pair{"over the line table", string(big), string(replaceLine(replaceLine(big, 10, "ten\n"), 1400, "x\ny\n"))})
	random := rand.New(rand.NewSource(7))
	words := []string{"a\n", "b\n", "c\n", "a\r\n", "\n", "d", "tempo 120\n"}
	for i := 0; i < 300; i++ {
		lines := make([]string, random.Intn(30))
		for j := range lines {
			lines[j] = words[random.Intn(len(words)-1)]
		}
		from := strings.Join(lines, "")
		edited := append([]string(nil), lines...)
		for k := random.Intn(5); k >= 0; k-- {
			at := random.Intn(len(edited) + 1)
			switch random.Intn(3) {
			case 0:
				edited = append(edited[:at], append([]string{words[random.Intn(len(words))]}, edited[at:]...)...)
			case 1:
				if at < len(edited) {
					edited = append(edited[:at], edited[at+1:]...)
				}
			default:
				if at < len(edited) {
					edited[at] = words[random.Intn(len(words))]
				}
			}
		}
		pairs = append(pairs, pair{fmt.Sprintf("random %d", i), from, strings.Join(edited, "")})
	}
	for _, p := range pairs {
		hunks := DiffText([]byte(p.from), []byte(p.to))
		got, err := ApplyHunks([]byte(p.from), hunks)
		if err != nil || string(got) != p.to {
			t.Fatalf("%s: apply(from, diff) = %q, %v; want %q (hunks %+v)", p.name, got, err, p.to, hunks)
		}
		rebased, err := RebaseText([]byte(p.from), []byte(p.from), []byte(p.to))
		if err != nil || string(rebased) != p.to {
			t.Fatalf("%s: rebase on from = %q, %v", p.name, rebased, err)
		}
		if p.from == p.to && len(hunks) != 0 {
			t.Fatalf("%s: equal texts give hunks %+v", p.name, hunks)
		}
	}
}

// A patch made on one text applies to a text that another writer changed in
// other lines, and is refused, naming its line, when the same lines changed.
func TestPatchFollowsMovedTextAndRefusesTheSameLines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		from := numbered(30, newline)
		to := replaceLine(from, 12, "twelve"+newline)
		hunks := DiffText(from, to)
		if len(hunks) != 1 || hunks[0].Line != 9 {
			t.Fatalf("hunks %+v", hunks)
		}
		// Another writer inserted two lines at the top and changed line 25.
		moved := append([]byte("new 1"+newline+"new 2"+newline), replaceLine(from, 25, "changed"+newline)...)
		got, err := ApplyHunks(moved, hunks)
		want := append([]byte("new 1"+newline+"new 2"+newline), replaceLine(replaceLine(from, 25, "changed"+newline), 12, "twelve"+newline)...)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("moved text %q: %q, %v", newline, got, err)
		}
		// A context line changed: the hunk still applies by its changed line.
		context := replaceLine(from, 11, "eleven"+newline)
		got, err = ApplyHunks(context, hunks)
		if err != nil || !bytes.Equal(got, replaceLine(context, 12, "twelve"+newline)) {
			t.Fatalf("changed context %q: %q, %v", newline, got, err)
		}
		// The same line changed: refused, with the hunk's line.
		_, err = ApplyHunks(replaceLine(from, 12, "mine"+newline), hunks)
		var mismatch *HunkError
		if !errors.As(err, &mismatch) || mismatch.Hunk != 0 || mismatch.Line != 9 || !strings.Contains(err.Error(), "line 9") {
			t.Fatalf("same line %q: %v", newline, err)
		}
	}
}

func TestPatchRefusesAmbiguousOverlappingAndOutOfRangeHunks(t *testing.T) {
	text := []byte("a\nb\na\nb\nc\n")
	var mismatch *HunkError
	// "a\nb\n" is at lines 1 and 3; line 2 is as near to each.
	if _, err := ApplyHunks(text, []Hunk{{Line: 2, Old: "a\nb\n", New: "x\n"}}); !errors.As(err, &mismatch) || !strings.Contains(err.Error(), "more than one place") {
		t.Fatalf("ambiguous: %v", err)
	}
	if got, err := ApplyHunks(text, []Hunk{{Line: 3, Old: "a\nb\n", New: "x\n"}}); err != nil || string(got) != "a\nb\nx\nc\n" {
		t.Fatalf("nearest of two: %q, %v", got, err)
	}
	if _, err := ApplyHunks(text, []Hunk{{Line: 1, Old: "a\nb\na\n", New: "x\n"}, {Line: 3, Old: "a\nb\n", New: "y\n"}}); !errors.As(err, &mismatch) || mismatch.Hunk != 1 || !strings.Contains(err.Error(), "overlaps hunk 1") {
		t.Fatalf("overlap: %v", err)
	}
	if _, err := ApplyHunks(text, []Hunk{{Line: 7, Old: "", New: "z\n"}}); !errors.As(err, &mismatch) || mismatch.Line != 7 {
		t.Fatalf("insertion past the end: %v", err)
	}
	if got, err := ApplyHunks(text, []Hunk{{Line: 6, Old: "", New: "z\n"}, {Line: 6, Old: "", New: "w\n"}}); err != nil || string(got) != "a\nb\na\nb\nc\nz\nw\n" {
		t.Fatalf("two insertions at one line keep their order: %q, %v", got, err)
	}
	if _, err := ApplyHunks(text, []Hunk{{Line: 0, Old: "a\n", New: "b\n"}}); !errors.As(err, &mismatch) {
		t.Fatalf("line 0: %v", err)
	}
}

const patchScore = "title \"Patch\"\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass=riff }\nsong { main }\n"

func TestPatchTextIntent(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := []byte(strings.ReplaceAll(patchScore, "\n", newline))
		tempo := strings.ReplaceAll(`"old":"tempo 120\n","new":"tempo 118\n"`, `\n`, map[string]string{"\n": `\n`, "\r\n": `\r\n`}[newline])
		result, err := applyM4Intent(source, `{"kind":"patchtext","hunks":[{"line":2,`+tempo+`}]}`, Options{})
		want := bytes.Replace(source, []byte("tempo 120"), []byte("tempo 118"), 1)
		if err != nil || !bytes.Equal(result.Source, want) || len(result.Files) != 0 || result.Label != "Source saved" {
			t.Fatalf("entry patch %q: %+v, %v", newline, result, err)
		}
		// The text moved: the patch still applies.
		moved := append([]byte("// note"+newline), source...)
		if result, err = applyM4Intent(moved, `{"kind":"patchtext","hunks":[{"line":2,`+tempo+`}]}`, Options{}); err != nil || !bytes.Contains(result.Source, []byte("tempo 118")) {
			t.Fatalf("moved entry %q: %v", newline, err)
		}
		// The line changed: refused, naming the hunk's line.
		changed := bytes.Replace(source, []byte("tempo 120"), []byte("tempo 100"), 1)
		_, err = applyM4Intent(changed, `{"kind":"patchtext","hunks":[{"line":2,`+tempo+`}]}`, Options{})
		var reason *ReasonError
		var located *IntentError
		if !errors.As(err, &reason) || reason.Reason != "hunk_mismatch" || reason.Field != "hunks" || !strings.Contains(err.Error(), "line 2") || !errors.As(err, &located) || located.Field != "hunks" {
			t.Fatalf("changed line %q: %v", newline, err)
		}
	}
	// Each hunk names its line, its old lines and its new lines, even when
	// empty, so a forgotten field is refused rather than read as "insert".
	for _, missing := range []string{"line", "old", "new"} {
		hunk := map[string]any{"line": 2, "old": "tempo 120\n", "new": "tempo 118\n"}
		delete(hunk, missing)
		raw, _ := json.Marshal(map[string]any{"kind": "patchtext", "hunks": []any{hunk}})
		_, err := applyM4Intent([]byte(patchScore), string(raw), Options{})
		var located *IntentError
		if !errors.As(err, &located) || located.Field != "hunks."+missing {
			t.Fatalf("without %s: %v", missing, err)
		}
	}
	for name, raw := range map[string]string{
		"no hunks":      `{"kind":"patchtext","hunks":[]}`,
		"unknown field": `{"kind":"patchtext","hunks":[{"line":2,"old":"tempo 120\n","new":"tempo 118\n","at":3}]}`,
		"line 0":        `{"kind":"patchtext","hunks":[{"line":0,"old":"tempo 120\n","new":"tempo 118\n"}]}`,
	} {
		if _, err := applyM4Intent([]byte(patchScore), raw, Options{}); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// Insertion and deletion use an empty old or new.
	result, err := applyM4Intent([]byte(patchScore), `{"kind":"patchtext","hunks":[{"line":1,"old":"","new":"// first\n"},{"line":2,"old":"tempo 120\n","new":""}]}`, Options{})
	if err != nil || string(result.Source) != "// first\n"+strings.Replace(patchScore, "tempo 120\n", "", 1) {
		t.Fatalf("insert and delete: %+v, %v", result, err)
	}
}

// patchProject is a score with a second source file, as a host supplies it.
func patchProject(newline string) ([]byte, []byte, Options) {
	// parseCompiler parses only the entry, so the entry stands alone.
	main := []byte(strings.ReplaceAll("title \"Patch\"\ntempo 120\ntrack bass acid {}\npattern lead { a1 . }\nscene main { bass=lead }\nsong { main }\n", "\n", newline))
	part := []byte(strings.ReplaceAll("// part\npattern riff { a1 . c2 . }\n", "\n", newline))
	files := []notation.SourceFile{{Path: "/song/main.cicada", Source: main}, {Path: "/song/parts/riff.cicada", Source: part}, {Path: "/lib/std/kit.cicada", Source: []byte("// library\n"), Library: "std/kit"}}
	return main, part, Options{Compiler: parseCompiler{}, Path: "/song/main.cicada", Sources: files}
}

func TestPatchTextPatchesAnotherFile(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		main, part, opts := patchProject(newline)
		hunks := DiffText(part, bytes.Replace(part, []byte("a1 . c2 ."), []byte("a1 a1 c2 ."), 1))
		want := bytes.Replace(part, []byte("a1 . c2 ."), []byte("a1 a1 c2 ."), 1)
		for _, path := range []string{"/song/parts/riff.cicada", "parts/riff.cicada"} {
			// Two intents patch the file in turn: one staged change, from the
			// first Before to the last After.
			first, second := hunks, DiffText(want, bytes.Replace(want, []byte("// part"), []byte("// riff"), 1))
			result, err := Apply(main, Envelope{Version: 1, Intents: []Intent{&PatchText{Path: path, Hunks: first}, &PatchText{Path: path, Hunks: second}}}, opts)
			final := bytes.Replace(want, []byte("// part"), []byte("// riff"), 1)
			if err != nil || !bytes.Equal(result.Source, main) || len(result.Files) != 1 || result.Files[0].Path != "/song/parts/riff.cicada" || !bytes.Equal(result.Files[0].Before, part) || !bytes.Equal(result.Files[0].After, final) {
				t.Fatalf("%s %q: %+v, %v", path, newline, result, err)
			}
		}
		var reason *ReasonError
		if _, err := Apply(main, Envelope{Version: 1, Intents: []Intent{&PatchText{Path: "parts/other.cicada", Hunks: hunks}}}, opts); !errors.As(err, &reason) || reason.Reason != "unknown_file" {
			t.Fatalf("unknown file: %v", err)
		}
		if _, err := Apply(main, Envelope{Version: 1, Intents: []Intent{&PatchText{Path: "/lib/std/kit.cicada", Hunks: []Hunk{{Line: 1, Old: "// library\n", New: "// mine\n"}}}}}, opts); !errors.As(err, &reason) || reason.Reason != "library_file" {
			t.Fatalf("library file: %v", err)
		}
		stale := bytes.Replace(part, []byte("a1 . c2 ."), []byte("e1 . c2 ."), 1)
		opts.Sources[1].Source = stale
		_, err := Apply(main, Envelope{Version: 1, Intents: []Intent{&PatchText{Path: "parts/riff.cicada", Hunks: hunks}}}, opts)
		if !errors.As(err, &reason) || reason.Reason != "hunk_mismatch" || reason.File != "/song/parts/riff.cicada" || !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("changed line of another file: %v", err)
		}
	}
}

// The patch never changes the supplied buffers: the text is copied before it
// is patched.
func TestPatchLeavesItsInputs(t *testing.T) {
	from := []byte("a\nb\nc\n")
	hunks := DiffText(from, []byte("a\nB\nc\n"))
	before := bytes.Clone(from)
	if _, err := ApplyHunks(from, hunks); err != nil || !bytes.Equal(from, before) {
		t.Fatalf("input changed: %q, %v", from, err)
	}
}
