package edit

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPatchSpansAppliesDisjointSpansFromTheEnd(t *testing.T) {
	got, err := PatchSpans([]byte("aaa bbb ccc"), []Span{{0, 3, "X"}, {8, 11, "ZZZZ"}})
	if err != nil || string(got) != "X bbb ZZZZ" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestPatchSpansRejectsOverlap(t *testing.T) {
	_, err := PatchSpans([]byte("aaa bbb ccc"), []Span{{0, 5, "X"}, {4, 6, "Y"}})
	if err == nil || err.Error() != "overlapping or invalid source patches" {
		t.Fatalf("%v", err)
	}
	if _, err := PatchSpans([]byte("abc"), []Span{{2, 9, "X"}}); err == nil {
		t.Fatal("out of range accepted")
	}
}

func TestReplaceSpanErrorText(t *testing.T) {
	if _, err := ReplaceSpan([]byte("abc"), 2, 1, nil); err == nil || err.Error() != "song source span is invalid" {
		t.Fatalf("%v", err)
	}
}

func TestOffsetCountsRunesNotBytes(t *testing.T) {
	source := []byte("aéb\ncd\n")
	if got := Offset(source, notation.Position{Line: 1, Column: 3}); got != 3 { // after a, e-acute (2 bytes)
		t.Fatalf("column offset %d", got)
	}
	if got := Offset(source, notation.Position{Line: 2, Column: 2}); got != strings.Index(string(source), "d") {
		t.Fatalf("line offset %d", got)
	}
}

func TestNewlinePrefersCRLF(t *testing.T) {
	if Newline([]byte("a\r\nb\r\n")) != "\r\n" || Newline([]byte("a\nb\n")) != "\n" {
		t.Fatal("newline detection")
	}
}
