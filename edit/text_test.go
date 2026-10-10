package edit

import (
	"bytes"
	"strings"
	"testing"
)

func TestReplaceTextAndDeclaration(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"replacetext","source":"title \"Whole score\"\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene main { bass=pulse }\nsong { main }\n"}`,
		`{"kind":"replacedeclaration","declaration":"title_decl","text":"title \"Whole score\""}`,
	} {
		got, err := applyM4Intent([]byte(songScore), raw, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := "title \"Whole score\"\ntrack bass acid {}\npattern pulse { 1 . 5 . }\nscene main { bass=pulse }\nsong { main }\n"
		if strings.Contains(raw, "replacedeclaration") {
			want = strings.Replace(songScore, `title "Song lane"`, `title "Whole score"`, 1)
		}
		if string(got.Source) != want || got.Label != "Source saved" {
			t.Fatalf("%s: got %q / %q", raw, got.Source, got.Label)
		}
	}
	got, err := applyM4Intent([]byte(songScore), `{"kind":"replacedeclaration","declaration":"pattern","name":"pulse","text":"pattern pulse { 2 . 6 . }"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := bytes.Replace([]byte(songScore), []byte("pattern pulse { 1 . 5 . }"), []byte("pattern pulse { 2 . 6 . }"), 1); !bytes.Equal(got.Source, want) {
		t.Fatalf("declaration bytes %q", got.Source)
	}
}

func TestReplaceDeclarationRejectsWrongOrMissingTarget(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"replacedeclaration","declaration":"pattern","name":"missing","text":"pattern missing { 1 }"}`,
		`{"kind":"replacedeclaration","declaration":"pattern","name":"pulse","text":"pattern other { 1 }"}`,
		`{"kind":"replacedeclaration","declaration":"pattern","name":"pulse","text":"pattern pulse { 1 }\nseed 4"}`,
		`{"kind":"replacedeclaration","declaration":"pattern","name":"pulse","text":"seed 4"}`,
	} {
		if _, err := applyM4Intent([]byte(songScore), raw, Options{}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
