package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckGraphDelayDiagnostics(t *testing.T) {
	for _, tc := range []struct{ expression, params, note, code string }{
		{"delay(noise(), 440Hz)", "", "a3", "CICADA-UNIT"},
		{"delay(noise(), 100ms)", "", "a3", "CICADA-PARAM"},
		{"delay(noise(), time)", "time = 100ms", "a3", "CICADA-PARAM"},
		{"comb(noise(), 2 / pitch, 0.99, 0.5)", "", "c0", "CICADA-PARAM"},
		{"delay(delay(delay(noise(), 1ms), 2ms), 3ms)", "", "a3", "CICADA-LIMIT"},
	} {
		path := filepath.Join(t.TempDir(), "invalid.cicada")
		source := fmt.Sprintf("instrument sound { param time: ms = 10ms voice mono { out = %s } } track t sound { %s } pattern p notes { %s } scene s { t=p } song { s }", tc.expression, tc.params, tc.note)
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if err := checkCommand([]string{path}, &stdout, &stderr); err == nil {
			t.Fatalf("invalid delay accepted: %s %s %s", tc.expression, tc.params, tc.note)
		}
		if !bytes.Contains(stderr.Bytes(), []byte(tc.code)) {
			t.Fatalf("missing %s: %s", tc.code, &stderr)
		}
	}
}
