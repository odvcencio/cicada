package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"m31labs.dev/cicada/instrument"
)

func TestGraphDelayDiagnosticsAndHover(t *testing.T) {
	for _, tc := range []struct{ expression, code string }{
		{"delay(noise(), 440Hz)", "CICADA-UNIT"},
		{"comb(noise(), 100ms, 0.99, 0.5)", "CICADA-PARAM"},
		{"delay(delay(delay(noise(), 1ms), 2ms), 3ms)", "CICADA-LIMIT"},
	} {
		source := fmt.Sprintf("instrument sound { voice mono { out = %s } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)
		var output bytes.Buffer
		s := server{out: &output, documents: map[string][]byte{"untitled:pluck": []byte(source)}}
		if err := s.publish("untitled:pluck"); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(output.Bytes(), []byte(tc.code)) {
			t.Fatalf("missing %s: %s", tc.code, output.Bytes())
		}
	}
	for _, name := range []string{"delay", "comb"} {
		source := []byte("instrument sound { voice mono { out = " + name + "(noise(), 10ms, 0.99, 0.5) } }")
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
