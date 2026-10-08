package edit

import (
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

// parseCompiler validates a candidate with the notation parser only.
type parseCompiler struct{}

func (parseCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
	_, ds := notation.Parse(source)
	if hasErrors(ds) {
		var lines []string
		for _, d := range ds {
			if d.Severity == "error" {
				lines = append(lines, d.Message)
			}
		}
		return nil, errors.New(strings.Join(lines, "; "))
	}
	return &Plan{Revision: Revision(source)}, nil
}

// setParamFixture builds a score with one authored instrument and a track
// that uses it, and returns the name of the instrument's first parameter.
func setParamFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	patch, ok := instrument.FindPatch("warm-pad")
	if !ok {
		t.Fatal("warm-pad patch missing")
	}
	decl, err := patch.Source("my-pad")
	if err != nil {
		t.Fatal(err)
	}
	fixture := "cicada 1\ntitle \"Keep my title\"\n// authored track\n" + decl + "\ntrack keys my-pad {}\ntrack bass acid {}\npattern p acid { 1 . 5 . }\nscene main { bass=p }\nsong { main*2 }\n"
	score, ds := notation.Parse([]byte(fixture))
	if score == nil || hasErrors(ds) || len(score.Instruments) == 0 || len(score.Instruments[0].Params) == 0 {
		t.Fatalf("fixture invalid: %+v", ds)
	}
	return []byte(fixture), score.Instruments[0].Params[0].Name
}
