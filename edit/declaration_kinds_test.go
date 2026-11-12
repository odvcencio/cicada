package edit

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDeclarationKindsFollowTheGrammar keeps the declaration values of
// replacedeclaration equal to the grammar's top-level declarations, so the
// catalog lists every type the handler can replace and no other.
func TestDeclarationKindsFollowTheGrammar(t *testing.T) {
	data, err := os.ReadFile("../language/grammar/grammar.go")
	if err != nil {
		t.Fatal(err)
	}
	_, rule, found := strings.Cut(string(data), `g.Define("_declaration", choice(`)
	if !found {
		t.Fatal("language/grammar/grammar.go has no _declaration rule")
	}
	rule, _, _ = strings.Cut(rule, "))")
	var want []string
	for _, match := range regexp.MustCompile(`sym\("([a-z_]+)"\)`).FindAllStringSubmatch(rule, -1) {
		want = append(want, match[1])
	}
	if got := strings.Join(declarationKinds[1:], " "); declarationKinds[0] != "pattern" || got != strings.Join(want, " ") {
		t.Fatalf("declaration values %v; the grammar's top-level declarations are pattern then %v", declarationKinds, want)
	}
}
