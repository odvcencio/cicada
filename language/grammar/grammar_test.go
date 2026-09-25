package grammar

import (
	"bytes"
	"os"
	"testing"

	"github.com/odvcencio/gotreesitter/grammargen"
)

// TestBlobIsGenerated fails when notation/cicada.bin is stale. Regenerate
// it with `go generate ./notation`.
func TestBlobIsGenerated(t *testing.T) {
	blob, err := grammargen.Generate(Cicada())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../notation/cicada.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blob, committed) {
		t.Fatal("notation/cicada.bin differs from the grammar; run go generate ./notation")
	}
}

func TestGrammarValidatesAndPassesItsTests(t *testing.T) {
	g := Cicada()
	if warnings := grammargen.Validate(g); len(warnings) > 0 {
		t.Fatalf("grammar warnings: %v", warnings)
	}
	if err := grammargen.RunTests(g); err != nil {
		t.Fatal(err)
	}
}
