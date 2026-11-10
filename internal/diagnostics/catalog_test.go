package diagnostics

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

var codePattern = regexp.MustCompile(`CICADA-[A-Z0-9]+(?:-[A-Z0-9]+)*`)

const repositoryRoot = "../.."

// skippedDirs hold no emitting code: build output, history, dependencies,
// documentation, fixtures, and this catalog.
var skippedDirs = map[string]bool{".git": true, "build": true, "node_modules": true, ".recovery": true, "docs": true, "testdata": true, "dist": true}

// emittedCodes finds every code the repository's code can emit: CICADA-*
// inside a Go string literal of a non-test file, or anywhere in a non-test
// JavaScript or HTML file. A match followed by "-" is a prefix test such as
// strings.HasPrefix(code, "CICADA-LIB-"), not a code.
func emittedCodes(t *testing.T) map[string][]string {
	t.Helper()
	catalogDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	add := func(text, where string) {
		for _, at := range codePattern.FindAllStringIndex(text, -1) {
			if at[1] < len(text) && text[at[1]] == '-' {
				continue
			}
			found[text[at[0]:at[1]]] = append(found[text[at[0]:at[1]]], where)
		}
	}
	err = filepath.WalkDir(repositoryRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			absolute, _ := filepath.Abs(path)
			if skippedDirs[entry.Name()] || absolute == catalogDir {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		relative, _ := filepath.Rel(repositoryRoot, path)
		switch {
		case strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go"):
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					if text, err := strconv.Unquote(literal.Value); err == nil {
						add(text, relative)
					}
				}
				return true
			})
		case (strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".cjs") || strings.HasSuffix(name, ".mjs") || strings.HasSuffix(name, ".html")) && !strings.Contains(name, ".test."):
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			add(string(data), relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestEveryEmittedCodeIsCatalogued fails when code emits a CICADA-* code the
// catalog lacks, or when the catalog keeps a code that nothing emits.
func TestEveryEmittedCodeIsCatalogued(t *testing.T) {
	emitted := emittedCodes(t)
	if len(emitted) < 50 {
		t.Fatalf("found only %d codes; is the repository root right?", len(emitted))
	}
	for code, where := range emitted {
		if _, ok := Lookup(code); !ok {
			t.Errorf("%s is emitted (%s) but missing from the catalog in internal/diagnostics", code, strings.Join(where, ", "))
		}
	}
	for _, entry := range codes {
		if len(emitted[entry.Code]) == 0 {
			t.Errorf("the catalog lists %s, which no code emits", entry.Code)
		}
	}
}

func TestCatalogEntriesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for i, entry := range codes {
		if seen[entry.Code] {
			t.Errorf("%s is listed twice", entry.Code)
		}
		seen[entry.Code] = true
		if i > 0 && codes[i-1].Code >= entry.Code {
			t.Errorf("%s is out of alphabetical order", entry.Code)
		}
		if entry.Meaning == "" || entry.Fix == "" || len(entry.Severities) == 0 || len(entry.Scopes) == 0 {
			t.Errorf("%s lacks a meaning, fix, severity or scope", entry.Code)
		}
		for _, severity := range entry.Severities {
			if severity != "error" && severity != "warning" {
				t.Errorf("%s: unknown severity %q", entry.Code, severity)
			}
		}
		for _, scope := range entry.Scopes {
			if scope != "score" && scope != "command" && scope != "studio" && scope != "edit" {
				t.Errorf("%s: unknown scope %q", entry.Code, scope)
			}
		}
	}
}

// TestNotesReasonsMatchTheEditor keeps the CICADA-NOTES reasons equal to the
// reasons the edit package can return, and to the ones the manual documents.
func TestNotesReasonsMatchTheEditor(t *testing.T) {
	entry, ok := Lookup("CICADA-NOTES")
	if !ok {
		t.Fatal("CICADA-NOTES is missing")
	}
	var catalogued []string
	for _, reason := range entry.Reasons {
		catalogued = append(catalogued, reason.Reason)
		if reason.Meaning == "" {
			t.Errorf("reason %s has no meaning", reason.Reason)
		}
	}
	if strings.Join(catalogued, " ") != strings.Join(edits.NoteErrorCodes, " ") {
		t.Fatalf("CICADA-NOTES reasons\n%v\ndiffer from edit.NoteErrorCodes\n%v", catalogued, edits.NoteErrorCodes)
	}
	manual, err := os.ReadFile(filepath.Join(repositoryRoot, "docs", "manual", "editors.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range catalogued {
		if !bytes.Contains(manual, []byte("`"+reason+"`")) {
			t.Errorf("docs/manual/editors.md does not document the reason %s", reason)
		}
	}
}

const referencePage = "docs/manual/diagnostics.md"

// TestDiagnosticsReferenceIsCurrent regenerates docs/manual/diagnostics.md
// from the catalog and fails when the committed page differs.
// CICADA_UPDATE_DIAGNOSTICS_DOC=1 writes it instead.
func TestDiagnosticsReferenceIsCurrent(t *testing.T) {
	want := ReferencePage()
	path := filepath.Join(repositoryRoot, referencePage)
	if os.Getenv("CICADA_UPDATE_DIAGNOSTICS_DOC") == "1" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run CICADA_UPDATE_DIAGNOSTICS_DOC=1 go test ./internal/diagnostics -run TestDiagnosticsReferenceIsCurrent", referencePage)
	}
}

// TestDocumentedCodesExist fails when the documentation names a code that the
// catalog does not have, such as a renamed one.
func TestDocumentedCodesExist(t *testing.T) {
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "docs"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, at := range codePattern.FindAllStringIndex(text, -1) {
			code := text[at[0]:at[1]]
			if at[1] < len(text) && text[at[1]] == '-' {
				continue
			}
			if _, ok := Lookup(code); !ok {
				relative, _ := filepath.Rel(repositoryRoot, path)
				t.Errorf("%s names %s, which is not in the catalog", relative, code)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ExampleLookup() {
	entry, _ := Lookup("CICADA-SLIDE-REST")
	fmt.Println(entry.Severities, entry.Fix)
	// Output: [warning] slide into a note, or remove the slide
}

func TestCatalogIsSorted(t *testing.T) {
	catalog := Catalog()
	if !sort.SliceIsSorted(catalog, func(i, j int) bool { return catalog[i].Code < catalog[j].Code }) || len(catalog) != len(codes) {
		t.Fatal("Catalog is not the sorted catalog")
	}
}
