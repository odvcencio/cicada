package migration

import (
	"bytes"
	"testing"
)

func TestFixKeepsCommentInsideHoldBinding(t *testing.T) {
	source := []byte("track bass acid {}\npattern p { 1 . }\nscene main { bass=p }\nscene hold { bass // retain this direction\n = keep }\nsong { main hold }\n")
	fixed, _, err := FixSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fixed, []byte("// retain this direction")) {
		t.Fatalf("comment was lost: %s", fixed)
	}
}
