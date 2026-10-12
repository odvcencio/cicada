package edit

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPatchSpansAppliesDisjointSpansFromTheEnd(t *testing.T) {
	got, err := PatchSpans([]byte("aaa bbb ccc"), "main.cicada", []Span{{Start: 0, End: 3, Text: "X", File: "main.cicada"}, {Start: 8, End: 11, Text: "ZZZZ", File: "main.cicada"}})
	if err != nil || string(got) != "X bbb ZZZZ" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestPatchSpansRejectsOverlap(t *testing.T) {
	_, err := PatchSpans([]byte("aaa bbb ccc"), "", []Span{{Start: 0, End: 5, Text: "X"}, {Start: 4, End: 6, Text: "Y"}})
	if err == nil || err.Error() != "overlapping or invalid source patches" {
		t.Fatalf("%v", err)
	}
	if _, err := PatchSpans([]byte("abc"), "", []Span{{Start: 2, End: 9, Text: "X"}}); err == nil {
		t.Fatal("out of range accepted")
	}
}

func TestReplaceSpanErrorText(t *testing.T) {
	if _, err := ReplaceSpan([]byte("abc"), "", Span{Start: 2, End: 1}); err == nil || err.Error() != "song source span is invalid" {
		t.Fatalf("%v", err)
	}
}

func TestOffsetCountsRunesNotBytes(t *testing.T) {
	source := []byte("aéb\ncd\n")
	if got, err := Offset(source, "main.cicada", notation.Position{File: "main.cicada", Line: 1, Column: 3}); err != nil || got != 3 { // after a, e-acute (2 bytes)
		t.Fatalf("column offset %d: %v", got, err)
	}
	if got, err := Offset(source, "main.cicada", notation.Position{Line: 2, Column: 2}); err != nil || got != strings.Index(string(source), "d") {
		t.Fatalf("line offset %d: %v", got, err)
	}
}

func TestSourceCoordinatesRefuseForeignFiles(t *testing.T) {
	source := []byte("aéb\ncd\n")
	for _, file := range []string{"part.cicada", "other/main.cicada", ""} {
		if file != "" {
			if _, err := Offset(source, "main.cicada", notation.Position{File: file, Line: 1, Column: 3}); err == nil || !strings.Contains(err.Error(), "another source file") {
				t.Fatalf("foreign offset %q: %v", file, err)
			}
		}
		span := Span{Start: 1, End: 3, Text: "X", File: file}
		if result, err := ReplaceSpan(source, "main.cicada", span); result != nil || err == nil || !strings.Contains(err.Error(), "another source file") {
			t.Fatalf("foreign replacement %q: %q %v", file, result, err)
		}
		if result, err := PatchSpans(source, "main.cicada", []Span{{Start: 4, End: 5, Text: "Y", File: "main.cicada"}, span}); result != nil || err == nil || !strings.Contains(err.Error(), "another source file") {
			t.Fatalf("foreign patch %q: %q %v", file, result, err)
		}
	}
}

func TestNewlinePrefersCRLF(t *testing.T) {
	if Newline([]byte("a\r\nb\r\n")) != "\r\n" || Newline([]byte("a\nb\n")) != "\n" {
		t.Fatal("newline detection")
	}
}
