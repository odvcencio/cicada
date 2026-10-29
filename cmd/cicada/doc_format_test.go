package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

// The Writing music chapter opens with a score and then tells the reader to run
// `cicada fmt --check` on it. Most blocks in the manual use compact one-line
// forms and are only checked for validity, so this opening block is the one that
// has to be exactly what `cicada fmt` writes.
func TestWritingMusicOpeningScoreIsFormatted(t *testing.T) {
	path := filepath.Join(repositoryRoot(), "docs", "manual", "writing-music.md")
	examples, err := readDocumentationExamples(path)
	if err != nil || len(examples) == 0 {
		t.Fatalf("no Cicada examples in %s: %v", path, err)
	}
	opening := examples[0]
	document, err := notation.ParseDocument([]byte(opening.source))
	if err != nil {
		t.Fatalf("%s:%d: %v", opening.path, opening.line, err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(opening.source), formatted) {
		t.Fatalf("%s:%d: the opening score is followed by `cicada fmt --check`, so it must already be formatted; use this text:\n%s", opening.path, opening.line, formatted)
	}
}
