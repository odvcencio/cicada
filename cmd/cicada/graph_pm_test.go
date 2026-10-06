package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckGraphPMDiagnostics(t *testing.T) {
	for _, tc := range []struct{ expression, code string }{
		{"pm(1ms, sine(pitch), 1)", "CICADA-UNIT"},
		{"pm(pitch, 1, 1)", "CICADA-UNIT"},
		{"pm(pitch, sine(pitch), 1Hz)", "CICADA-UNIT"},
		{"pm(pitch, sine(pitch))", "CICADA-PARAM"},
	} {
		path := filepath.Join(t.TempDir(), "invalid.cicada")
		source := fmt.Sprintf("instrument sound { voice mono { out = %s } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if err := checkCommand([]string{path}, &stdout, &stderr); err == nil {
			t.Fatalf("invalid PM accepted: %s", tc.expression)
		}
		if !bytes.Contains(stderr.Bytes(), []byte(tc.code)) {
			t.Fatalf("missing %s: %s", tc.code, &stderr)
		}
	}
}
