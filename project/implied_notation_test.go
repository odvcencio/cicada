package project

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestImplicitAcidKindAndKeepAreCanonicalNoOps(t *testing.T) {
	source := []byte("track bass acid {}\npattern riff acid { 1 . }\nscene main { bass = riff }\nscene hold {}\nsong { main hold }\n")
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("source diagnostics: %+v", diagnostics)
	}
	canonical, diagnostics := FromScore(score)
	if canonical == nil || len(diagnostics) != 0 {
		t.Fatalf("project diagnostics: %+v", diagnostics)
	}
	legacy := *canonical
	legacy.Patterns = append([]Pattern(nil), canonical.Patterns...)
	legacy.Patterns[0].Kind = "notes"
	legacy.Scenes = append([]Scene(nil), canonical.Scenes...)
	legacy.Scenes[1].Bindings = map[string]string{"bass": "keep"}
	if !SemanticEqual(canonical, &legacy) {
		left, leftErr := CanonicalJSON(canonical)
		right, rightErr := CanonicalJSON(&legacy)
		t.Fatalf("explicit melodic kind or scene hold changed musical meaning: %v %v\n%s\n%s", leftErr, rightErr, left, right)
	}
	encoded, err := CanonicalJSON(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"keep"`)) || !bytes.Contains(encoded, []byte(`"kind": "acid"`)) {
		t.Fatalf("canonical JSON retained implied values: %s", encoded)
	}
	generated, err := ToSource(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	again, diagnostics := notation.Parse(generated)
	if len(diagnostics) != 0 {
		t.Fatalf("generated source diagnostics: %+v", diagnostics)
	}
	converted, diagnostics := FromScore(again)
	if converted == nil || len(diagnostics) != 0 || !SemanticEqual(&legacy, converted) {
		t.Fatalf("JSON to source round trip changed meaning: %+v", diagnostics)
	}
}
