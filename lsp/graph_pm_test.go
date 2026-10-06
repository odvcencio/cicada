package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"m31labs.dev/cicada/instrument"
)

func TestGraphPMDiagnosticsAndHover(t *testing.T) {
	for _, tc := range []struct{ expression, code string }{
		{"pm(1ms, sine(pitch), 1)", "CICADA-UNIT"},
		{"pm(pitch, 1, 1)", "CICADA-UNIT"},
		{"pm(pitch, sine(pitch), 1Hz)", "CICADA-UNIT"},
		{"pm(pitch, sine(pitch))", "CICADA-PARAM"},
	} {
		source := fmt.Sprintf("instrument sound { voice mono { out = %s } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)
		var output bytes.Buffer
		s := server{out: &output, documents: map[string][]byte{"untitled:pm": []byte(source)}}
		if err := s.publish("untitled:pm"); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(output.Bytes(), []byte(tc.code)) {
			t.Fatalf("missing %s: %s", tc.code, output.Bytes())
		}
	}
	for _, name := range []string{"pm"} {
		source := []byte("instrument sound { voice mono { out = " + name + "(pitch, sine(pitch), 4) } }")
		at := bytes.Index(source, []byte(name+"("))
		value, err := json.Marshal(hover(source, utf16Position(source, at)))
		if err != nil {
			t.Fatal(err)
		}
		operation, _ := instrument.Operation(name)
		meaning, _ := json.Marshal(operation.Meaning)
		if !bytes.Contains(value, meaning[1:len(meaning)-1]) {
			t.Fatalf("hover differs from explain for %s: %s", name, value)
		}
	}
}
