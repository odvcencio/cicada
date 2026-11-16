package edit

import (
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestLibraryInsertComparesFullDiagnostics(t *testing.T) {
	source := []byte("cicada 2\ntempo 120\ntrack lead acid {}\npattern melody { a1 . c2 . }\nscene main { lead = melody }\nsong { main }\n")
	check := func(candidate []byte, _ map[string][]byte) ([]notation.Diagnostic, error) {
		if strings.Contains(string(candidate), "track lead piano") {
			return []notation.Diagnostic{{Severity: "error", Code: "PACK", Message: "note outside pack zones"}}, nil
		}
		return nil, nil
	}
	_, err := Apply(source, Envelope{Version: 1, Revision: Revision(source), Intents: []Intent{&InsertLibraryItem{Track: "lead", ImportPath: "demo/voices", Reference: "piano", ItemKind: "instrument"}}}, Options{Compiler: parseCompiler{}, CheckAll: check})
	var refusal *ReasonError
	if !errors.As(err, &refusal) || refusal.Reason != "voice_change_invalid" || !strings.Contains(strings.Join(refusal.Uses, " "), "outside pack zones") {
		t.Fatalf("%v", err)
	}
}
