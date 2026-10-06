package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/language/grammar"
)

func TestExperimentalPolyphonyDocumentationContainsNoVerificationSnapshot(t *testing.T) {
	path := filepath.Join(repositoryRoot(), "docs", "experimental-polyphony.md")
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	measurements := regexp.MustCompile(`(?i)(?:raw|brotli|worklet)\s*(?:is\s*)?\d+\s*/\s*\d+\s*bytes`)
	qualification := regexp.MustCompile(`(?i)\b(?:remain|remains|are|is)\s+unverified\b`)
	if measurements.Match(doc) || qualification.Match(doc) {
		t.Fatal("keep build measurements and qualification status in PR descriptions or CI artifacts")
	}
}

type documentationExample struct {
	path     string
	line     int
	kind     string
	expected string
	source   string
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestDocumentationCicadaExamples(t *testing.T) {
	root := repositoryRoot()
	var examples []documentationExample
	readmeExamples, err := readDocumentationExamples(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	examples = append(examples, readmeExamples...)
	for _, dir := range []string{"docs/spec", "docs/manual"} {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			found, err := readDocumentationExamples(path)
			if err != nil {
				return err
			}
			examples = append(examples, found...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	valid, invalid := 0, 0
	temp := t.TempDir()
	if err := os.Mkdir(filepath.Join(temp, "audio"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp, "audio", "example.wav"), testwav.Bytes(48000, 1, 16, 4800, 1), 0600); err != nil {
		t.Fatal(err)
	}
	for index, example := range examples {
		path := filepath.Join(temp, fmt.Sprintf("example-%03d.cicada", index))
		if err := os.WriteFile(path, []byte(example.source), 0o600); err != nil {
			t.Fatal(err)
		}
		inspection, err := inspectScore(path)
		if err != nil {
			t.Errorf("%s:%d: validator could not read example: %v", example.path, example.line, err)
			continue
		}
		var errors []string
		for _, diagnostic := range inspection.diagnostics {
			if diagnostic.Severity == "error" {
				errors = append(errors, diagnostic.Code)
			}
		}
		switch example.kind {
		case "cicada":
			valid++
			if len(errors) != 0 {
				t.Errorf("%s:%d: valid example produced errors %v", example.path, example.line, errors)
			}
		case "cicada-invalid":
			invalid++
			if len(errors) != 1 || errors[0] != example.expected {
				t.Errorf("%s:%d: expected only %s, got %v", example.path, example.line, example.expected, errors)
			}
		}
	}
	if valid+invalid == 0 {
		t.Fatal("no Cicada examples found under docs/spec or docs/manual")
	}
	t.Logf("checked %d Cicada blocks: %d valid, %d invalid", valid+invalid, valid, invalid)
}

func TestDocumentationExamplesUseEditionTwo(t *testing.T) {
	root := repositoryRoot()
	var examples []documentationExample
	for _, path := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "docs", "spec"), filepath.Join(root, "docs", "manual")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			found, err := readDocumentationExamples(path)
			if err != nil {
				t.Fatal(err)
			}
			examples = append(examples, found...)
			continue
		}
		if err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			found, err := readDocumentationExamples(path)
			if err != nil {
				return err
			}
			examples = append(examples, found...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, example := range examples {
		if example.kind == "cicada-invalid" || example.kind == "cicada" {
			first := ""
			for _, line := range strings.Split(example.source, "\n") {
				if strings.TrimSpace(line) != "" {
					first = strings.TrimSpace(line)
					break
				}
			}
			if first != "cicada 2" {
				t.Errorf("%s:%d: Cicada example must declare edition 2", example.path, example.line)
			}
		}
	}
}

func readDocumentationExamples(path string) ([]documentationExample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	ticks := strings.Repeat(string(rune(96)), 3)
	var examples []documentationExample
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, ticks) {
			continue
		}
		info := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, ticks)))
		if len(info) == 0 {
			continue
		}
		kind := info[0]
		if kind != "cicada" && kind != "cicada-invalid" {
			continue
		}
		startLine := i + 1
		expected := ""
		if kind == "cicada-invalid" {
			if len(info) != 2 || !strings.HasPrefix(info[1], "CICADA-") {
				return nil, fmt.Errorf("%s:%d: invalid example fence must name one CICADA diagnostic", path, startLine)
			}
			expected = info[1]
		}
		i++
		start := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != ticks {
			i++
		}
		if i == len(lines) {
			return nil, fmt.Errorf("%s:%d: unterminated Cicada example", path, startLine)
		}
		examples = append(examples, documentationExample{
			path: path, line: startLine, kind: kind, expected: expected,
			source: strings.Join(lines[start:i], "\n") + "\n",
		})
	}
	return examples, nil
}

func TestDocumentationEBNFMatchesGrammarDSL(t *testing.T) {
	path := filepath.Join(repositoryRoot(), "docs", "spec", "appendix.ebnf")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := grammar.EBNF()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("appendix is out of sync with the grammargen DSL; run go generate ./language/grammar")
	}
}
