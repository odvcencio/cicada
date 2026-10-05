package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const studioPatternScore = `title "Precise edits"
key a minor
track bass acid {}
track drums drums {}
phrase hook { 1^ . 5~*2?70 - }
pattern p acid slot=7 { swing = 54% use hook use hook +12 }
pattern shared { use hook }
pattern beat drums { bd: x.Xx3; }
scene main { bass = p drums=beat }
scene other { bass=shared }
song { main*2 other }
`

func patternProject(t *testing.T, source []byte) *project.Project {
	t.Helper()
	score, ds := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(ds) {
		t.Fatalf("parse: %+v\n%s", ds, source)
	}
	p, ds := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(ds) {
		t.Fatalf("compile: %+v\n%s", ds, source)
	}
	return p
}

func TestPatternVariationIsIndependentPreservesSoundAndDoesNotCopySlot(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := strings.Replace(studioPatternScore, "use hook +12 }", "use hook +12 } // parent comment", 1)
		before := []byte(strings.ReplaceAll(source, "\n", newline))
		updated, err := duplicatePatternSource(before, "p", "variation")
		if err != nil {
			t.Fatal(err)
		}
		p := patternProject(t, updated)
		parent, clone := p.Patterns[0], p.Patterns[1]
		parent.ID = clone.ID
		if !reflect.DeepEqual(parent, clone) {
			t.Fatalf("variation changed playback: parent=%+v clone=%+v", parent, clone)
		}
		node, _, err := studioDeclaration(updated, studioPatternTypes, "variation")
		if err != nil {
			t.Fatal(err)
		}
		text := string(updated[node.StartByte():node.EndByte()])
		if strings.Contains(text, "use hook") || strings.Contains(text, "slot") {
			t.Fatalf("variation retained shared phrase or reserved slot: %s", text)
		}
		if !bytes.Contains(updated, []byte("use hook +12 } // parent comment"+newline)) || !bytes.Contains(updated, []byte("phrase hook { 1^ . 5~*2?70 - }")) {
			t.Fatal("parent or its comment changed")
		}
		if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(updated, []byte(newline), nil), []byte("\n")) {
			t.Fatal("clone changed line endings")
		}
		if _, err := duplicatePatternSource(updated, "p", "variation"); err == nil {
			t.Fatal("duplicate name accepted")
		}
	}
}

func TestPrecisionStepEditDoesNotChangeSharedPhrase(t *testing.T) {
	before := []byte(studioPatternScore)
	original := patternProject(t, before)
	updated, err := patternStepSource(before, "p", "", 4, &studioStepEdit{Mode: "note", Pitch: 72, Accent: true, Slide: true, Ratchet: 8, Chance: 23})
	if err != nil {
		t.Fatal(err)
	}
	p := patternProject(t, updated)
	if !reflect.DeepEqual(p.Patterns[1], original.Patterns[1]) || !bytes.Contains(updated, []byte("phrase hook { 1^ . 5~*2?70 - }")) {
		t.Fatal("editing a phrase use changed another pattern")
	}
	want := &project.Step{Note: 72, Accent: true, Slide: true, Ratchet: 8, Probability: 23, Velocity: 100}
	if !reflect.DeepEqual(p.Patterns[0].Data[4], want) {
		t.Fatalf("note edit=%+v want=%+v", p.Patterns[0].Data[4], want)
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 7} {
		if !reflect.DeepEqual(p.Patterns[0].Data[index], original.Patterns[0].Data[index]) {
			t.Fatalf("unselected step %d changed", index)
		}
	}
	updated, err = patternStepSource(updated, "beat", "bd", 1, &studioStepEdit{Mode: "hit", Velocity: "4", Ratchet: 3, Chance: 50})
	if err != nil {
		t.Fatal(err)
	}
	p = patternProject(t, updated)
	hit := p.Patterns[2].Lanes["bd"][1]
	if hit == nil || hit.Velocity != 56 || hit.Ratchet != 3 || hit.Probability != 50 {
		t.Fatalf("drum dynamics=%+v", hit)
	}
	if _, err := patternStepSource(updated, "beat", "bd", 1, &studioStepEdit{Mode: "tie", Ratchet: 1, Chance: 100}); err == nil {
		t.Fatal("drum tie accepted")
	}
}

func TestPatternSettingsAndBindingPreserveAuthoredSource(t *testing.T) {
	source := strings.Replace(studioPatternScore, "swing = 54%", "swing = 54% // swing note\n", 1)
	updated, err := patternSettingsSource([]byte(source), "p", &studioPatternSettings{Swing100: 5725, Gate: 80, Transpose: -12})
	if err != nil {
		t.Fatal(err)
	}
	p := patternProject(t, updated)
	if p.Patterns[0].SwingPercent100 != 5725 || p.Patterns[0].GatePercent != 80 || p.Patterns[0].Transpose != -12 {
		t.Fatalf("settings=%+v", p.Patterns[0])
	}
	if !bytes.Contains(updated, []byte("swing = 57.25% // swing note")) || !bytes.Contains(updated, []byte("slot=7")) {
		t.Fatal("settings discarded source spelling or comments")
	}
	updated, err = bindPatternSource(updated, "main", "bass", "shared")
	if err != nil {
		t.Fatal(err)
	}
	p = patternProject(t, updated)
	if p.Scenes[0].Bindings["bass"] != "shared" || !bytes.Contains(updated, []byte("bass = shared drums=beat")) {
		t.Fatal("binding was not changed locally")
	}
	updated, err = bindPatternSource(updated, "other", "drums", "beat")
	if err != nil || patternProject(t, updated).Scenes[1].Bindings["drums"] != "beat" {
		t.Fatalf("missing scene binding was not inserted: %v", err)
	}
}

func TestPatternCommandsAreAtomicRevisionCheckedAndUndoable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pattern.cicada")
	if err := os.WriteFile(path, []byte(studioPatternScore), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	revision := studioRevision([]byte(studioPatternScore))
	response := studioCall(t, handler, "/api/pattern", studioEdit{Revision: revision, Action: "duplicate", Pattern: "p", NewName: "variation"})
	if response.Code != http.StatusOK {
		t.Fatalf("duplicate: %d %s", response.Code, response.Body.String())
	}
	content, _ := os.ReadFile(path)
	stale := studioCall(t, handler, "/api/pattern", studioEdit{Revision: revision, Action: "bind", Scene: "main", Track: "bass", Pattern: "variation"})
	if stale.Code != 409 {
		t.Fatalf("stale binding: %d", stale.Code)
	}
	bad := studioCall(t, handler, "/api/pattern", studioEdit{Revision: studioRevision(content), Action: "bind", Scene: "main", Track: "drums", Pattern: "variation"})
	if bad.Code != 422 {
		t.Fatalf("mismatched track: %d %s", bad.Code, bad.Body.String())
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(content, unchanged) {
		t.Fatal("failed binding mutated the score")
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(content)})
	restored, _ := os.ReadFile(path)
	if undo.Code != 200 || !bytes.Equal(restored, []byte(studioPatternScore)) {
		t.Fatalf("variation cannot undo: %d %s", undo.Code, undo.Body.String())
	}
}

func TestPatternRangesPreserveDynamicsAndOverlapCopies(t *testing.T) {
	before := []byte(studioPatternScore)
	original := patternProject(t, before)
	for _, operation := range []string{"copy", "reverse", "rotate", "transpose", "clear"} {
		edit := &studioPatternRange{Operation: operation, First: 0, Last: 3, Target: 2, Amount: 12}
		if operation == "rotate" {
			edit.Amount = -1
		}
		updated, err := patternRangeSource(before, "p", "", edit)
		if err != nil {
			t.Fatal(err)
		}
		p := patternProject(t, updated)
		if !reflect.DeepEqual(original.Patterns[1:], p.Patterns[1:]) {
			t.Fatalf("%s changed an unrelated pattern", operation)
		}
		steps := p.Patterns[0].Data
		switch operation {
		case "copy":
			if !reflect.DeepEqual(steps[2:6], original.Patterns[0].Data[:4]) {
				t.Fatal("overlapping copy read its partially overwritten destination")
			}
		case "reverse":
			for i := 0; i < 4; i++ {
				if !reflect.DeepEqual(steps[i], original.Patterns[0].Data[3-i]) {
					t.Fatal("reverse lost dynamics or ties")
				}
			}
		case "rotate":
			if !reflect.DeepEqual(steps[3], original.Patterns[0].Data[0]) || !reflect.DeepEqual(steps[0], original.Patterns[0].Data[1]) {
				t.Fatal("negative rotation lost the wraparound step")
			}
		case "transpose":
			if steps[0].Note != original.Patterns[0].Data[0].Note+12 || steps[2].Probability != 70 || steps[2].Ratchet != 2 || !steps[3].Tie || steps[1] != nil {
				t.Fatal("transpose changed articulation, ties, or rests")
			}
		case "clear":
			for _, step := range steps[:4] {
				if step != nil {
					t.Fatal("range did not clear")
				}
			}
		}
	}
	if _, err := patternRangeSource(before, "beat", "bd", &studioPatternRange{Operation: "transpose", First: 0, Last: 3, Amount: 1}); err == nil {
		t.Fatal("drum transpose accepted")
	}
	if _, err := patternRangeSource(before, "p", "", &studioPatternRange{Operation: "copy", First: 0, Last: 3, Target: 7}); err == nil {
		t.Fatal("copy past pattern end accepted")
	}
}

func TestPatternResizeUpdatesEveryLaneAndExplicitLength(t *testing.T) {
	before := []byte(strings.Replace(studioPatternScore, "pattern beat drums { bd: x.Xx3; }", "pattern beat drums steps=4 { bd: x.Xx3; sd: .x..; }", 1))
	for _, id := range []string{"p", "beat"} {
		updated, err := resizePatternSource(before, id, 64)
		if err != nil {
			t.Fatal(err)
		}
		p := patternProject(t, updated)
		index := 0
		if id == "beat" {
			index = 2
		}
		if p.Patterns[index].Steps != 64 {
			t.Fatal("pattern did not grow to maximum length")
		}
		updated, err = resizePatternSource(updated, id, 1)
		if err != nil || patternProject(t, updated).Patterns[index].Steps != 1 {
			t.Fatalf("pattern did not shrink: %v", err)
		}
		if id == "beat" && !bytes.Contains(updated, []byte("steps=1")) {
			t.Fatal("explicit length was not updated")
		}
	}
}

func TestPatternVariationKeepsCustomInstrumentOctave(t *testing.T) {
	before := []byte("key a minor\ninstrument synth { octave = 4 voice mono { out = saw(pitch) } }\ntrack lead synth {}\nphrase hook { 1 . 5~ }\npattern p { use hook 1' }\nscene main { lead=p }\nsong { main }\n")
	original := patternProject(t, before).Patterns[0]
	updated, err := duplicatePatternSource(before, "p", "variation")
	if err != nil {
		t.Fatal(err)
	}
	clone := patternProject(t, updated).Patterns[1]
	if !reflect.DeepEqual(original.Data, clone.Data) {
		t.Fatal("unassigned variation changed custom voice octave")
	}
	updated, err = bindPatternSource(updated, "main", "lead", "variation")
	if err != nil || !reflect.DeepEqual(original.Data, patternProject(t, updated).Patterns[1].Data) {
		t.Fatalf("assigned variation changed octave: %v", err)
	}
}
