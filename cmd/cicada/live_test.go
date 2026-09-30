package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveCheckDiagnostics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project live-test\ncicada 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.cicada")
	prefix := "track bass acid {}\nlive {\n"
	suffix := "\n}\npattern p { 1 }\nscene main { bass = p }\nsong { main }\n"
	tests := []struct {
		body, code string
		line, col  int
	}{
		{"macro intensity = 2", "CICADA-LIVE-MACRO", 3, 19},
		{"macro intensity = 0.3\nlayers intensity { missing >= 0.5 }", "CICADA-LIVE-TRACK", 4, 20},
	}
	var macros strings.Builder
	for i := 0; i < 17; i++ {
		fmt.Fprintf(&macros, "macro m%d = 0\n", i)
	}
	tests = append(tests, struct {
		body, code string
		line, col  int
	}{macros.String(), "CICADA-LIVE-LIMIT", 19, 1})
	for _, tt := range tests {
		if err := os.WriteFile(path, []byte(prefix+tt.body+suffix), 0600); err != nil {
			t.Fatal(err)
		}
		var output, diagnostics bytes.Buffer
		if err := checkPaths([]string{path}, &output, &diagnostics); err == nil {
			t.Fatalf("%s accepted", tt.code)
		}
		if got := diagnostics.String(); !strings.Contains(got, fmt.Sprintf("%s:%d:%d:", path, tt.line, tt.col)) || !strings.Contains(got, tt.code) {
			t.Fatalf("missing code or position: %s", got)
		}
	}
	example := filepath.Join(repositoryRoot(), "examples", "live-intensity.cicada")
	source, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	fixed, changed, err := fixSource(source)
	if err != nil || changed || !bytes.Equal(source, fixed) {
		t.Fatalf("fix changed valid edition-2 live score: %v", err)
	}
	var output, diagnostics bytes.Buffer
	if err := checkPaths([]string{example}, &output, &diagnostics); err != nil {
		t.Fatalf("example check: %v\n%s", err, diagnostics.String())
	}
}
