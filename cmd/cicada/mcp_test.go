package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/mcpwire"
)

// TestMCPToolListBudget keeps the tool list small enough to sit in a model's
// context: names, descriptions and input schemas within 6,000 bytes (about
// 1,500 tokens), the whole list within 24,000, in a fixed order.
func TestMCPToolListBudget(t *testing.T) {
	tools := mcpTools()
	brief, seen := 0, map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint {
			t.Errorf("tool %s: repeated or not marked read-only and closed-world", tool.Name)
		}
		seen[tool.Name] = true
		brief += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
	}
	whole, _ := json.Marshal(tools)
	t.Logf("tools/list: %d tools, %d bytes of names, descriptions and input schemas, %d bytes in all", len(tools), brief, len(whole))
	if brief > 6000 || len(whole) > 24000 {
		t.Fatalf("tool list is %d bytes brief, %d whole; budgets 6000 and 24000", brief, len(whole))
	}
	names := []string{}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, " ") != "song check intents explain library" {
		t.Fatalf("tools in order %v", names)
	}
}

// mcpDocument calls a tool in process and returns its document.
func mcpDocument(t *testing.T, name, arguments string) (map[string]any, bool) {
	t.Helper()
	result, err := mcpCall(name, json.RawMessage(arguments))
	if err != nil {
		t.Fatalf("%s %s: %v", name, arguments, err)
	}
	var document map[string]any
	if err := json.Unmarshal(result.Document, &document); err != nil {
		t.Fatalf("%s %s: %v", name, arguments, err)
	}
	return document, result.IsError
}

func documentError(document map[string]any) string {
	failure, _ := document["error"].(map[string]any)
	code, _ := failure["code"].(string)
	return code
}

// TestMCPToolsReturnTheCommandDocuments calls every tool in a new project and
// requires the documents the command line prints, with isError set when the
// command would exit with an error.
func TestMCPToolsReturnTheCommandDocuments(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root + "/demo")
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	if err := os.WriteFile("bad.cicada", []byte("cicada 2\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = rif }\nsong { main }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// check is check --json, byte for byte once compacted.
	document, isError := mcpDocument(t, "check", `{"score":"main.cicada"}`)
	var cli bytes.Buffer
	if err := checkCommand([]string{"--json", "main.cicada"}, &cli, io.Discard); err != nil || isError || document["schema"] != checkSchema || document["valid"] != true {
		t.Fatalf("check: %v %v %v", err, isError, document)
	}
	var fromCLI map[string]any
	if err := json.Unmarshal(cli.Bytes(), &fromCLI); err != nil {
		t.Fatal(err)
	}
	if fromTool, want := mustMarshal(document), mustMarshal(fromCLI); !bytes.Equal(fromTool, want) {
		t.Fatalf("check through the tool differs from check --json:\n%s\n%s", fromTool, cli.String())
	}
	if document, isError := mcpDocument(t, "check", `{"score":"bad.cicada"}`); !isError || document["schema"] != checkSchema || document["valid"] != false {
		t.Fatalf("check of a bad score: %v %v", isError, document)
	}

	if document, isError := mcpDocument(t, "song", `{}`); isError || document["schema"] != songSchema || len(document["scores"].([]any)) != 2 {
		t.Fatalf("song: %v %v", isError, document)
	}
	if document, isError := mcpDocument(t, "song", `{"score":"main.cicada","include":["symbols","structure"]}`); isError || document["revision"] == "" || document["source"] != nil || len(document["symbols"].([]any)) == 0 || document["structure"] == nil {
		t.Fatalf("song of a score: %v %v", isError, document)
	}

	if document, isError := mcpDocument(t, "intents", `{}`); isError || document["schema"] != intentsIndexSchema {
		t.Fatalf("intents: %v %v", isError, document)
	}
	if document, isError := mcpDocument(t, "intents", `{"kinds":["setparam","togglestep"]}`); isError || document["schema"] != intentsSchema || len(document["intents"].([]any)) != 2 {
		t.Fatalf("intents with kinds: %v %v", isError, document)
	}
	if document, isError := mcpDocument(t, "intents", `{"kinds":["nosuch"]}`); !isError || documentError(document) != "unknown_kind" {
		t.Fatalf("intents with an unknown kind: %v %v", isError, document)
	}

	if document, isError := mcpDocument(t, "explain", `{"diagnostic":"CICADA-PARAM"}`); isError || document["schema"] != diagnosticsSchema {
		t.Fatalf("explain diagnostic: %v %v", isError, document)
	}
	if document, isError := mcpDocument(t, "explain", `{"construct":"pattern"}`); isError || document["schema"] != explainSchema || document["fields"] == nil {
		t.Fatalf("explain construct: %v %v", isError, document)
	}
	if document, isError := mcpDocument(t, "explain", `{"construct":"pattern","diagnostic":"CICADA-PARAM"}`); !isError || documentError(document) != "usage" {
		t.Fatalf("explain with both: %v %v", isError, document)
	}

	if document, isError := mcpDocument(t, "library", `{}`); isError || document["schema"] != librarySchema || len(document["libraries"].([]any)) == 0 {
		t.Fatalf("library: %v %v", isError, document)
	}
	if document, isError := mcpDocument(t, "library", `{"path":"std/drums"}`); isError || document["library"].(map[string]any)["sha256"] == "" {
		t.Fatalf("library std/drums: %v %v", isError, document)
	}

	// Refusals: a path out of the root, a field no tool takes, an unknown
	// tool, arguments that are not an object.
	for _, arguments := range []string{`{"score":"../x.cicada"}`, `{"score":"/abs.cicada"}`, `{"score":"main.txt"}`} {
		if document, isError := mcpDocument(t, "song", arguments); !isError || documentError(document) != "path_outside_root" || document["schema"] != songSchema {
			t.Fatalf("song %s: %v %v", arguments, isError, document)
		}
	}
	if document, isError := mcpDocument(t, "check", `{"scores":"main.cicada"}`); !isError || documentError(document) != "usage" {
		t.Fatalf("an unknown field: %v %v", isError, document)
	}
	var toolErr *mcpwire.ToolError
	if _, err := mcpCall("nosuch", json.RawMessage(`{}`)); !errors.As(err, &toolErr) || toolErr.Code != mcpwire.CodeInvalidParams {
		t.Fatalf("an unknown tool: %v", err)
	}
	if _, err := mcpCall("check", json.RawMessage(`[]`)); !errors.As(err, &toolErr) {
		t.Fatalf("arguments that are not an object: %v", err)
	}
}

// mustMarshal encodes a decoded document with sorted keys, so two documents
// compare by value.
func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

// TestSongAndLibraryJSON covers song --json and lib list|show --json, and
// that explain --json names its format.
func TestSongAndLibraryJSON(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root + "/demo")
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	var stdout bytes.Buffer
	if err := songCommand([]string{"--json", "main.cicada"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var song songDocument
	source, _ := os.ReadFile("main.cicada")
	if err := json.Unmarshal(stdout.Bytes(), &song); err != nil || song.Schema != songSchema || song.File != "main.cicada" || song.Source == nil || *song.Source != string(source) || song.Revision == "" {
		t.Fatalf("song --json: %v %+v", err, song)
	}
	stdout.Reset()
	if err := songCommand([]string{"--json", "--include", "nosuch", "main.cicada"}, &stdout); err == nil || !strings.Contains(stdout.String(), `"code": "usage"`) {
		t.Fatalf("song --include nosuch: %v %s", err, stdout.String())
	}
	stdout.Reset()
	if err := songCommand([]string{"--json", "missing.cicada"}, &stdout); err == nil || !strings.Contains(stdout.String(), `"code": "unreadable_score"`) {
		t.Fatalf("song of a missing score: %v %s", err, stdout.String())
	}
	stdout.Reset()
	if err := libCommand([]string{"list", "--json"}, &stdout); err != nil || !strings.Contains(stdout.String(), `"schema": "cicada.library.v1"`) || !strings.Contains(stdout.String(), `"path": "std/drums"`) {
		t.Fatalf("lib list --json: %v %s", err, stdout.String())
	}
	stdout.Reset()
	if err := libCommand([]string{"show", "nosuch/lib", "--json"}, &stdout); err == nil || !strings.Contains(stdout.String(), `"code": "unknown_library"`) {
		t.Fatalf("lib show of an unknown library: %v %s", err, stdout.String())
	}
	stdout.Reset()
	if err := explain(&stdout, "pattern.steps", true); err != nil || !strings.Contains(stdout.String(), `"schema": "cicada.explain.v1"`) || !strings.Contains(stdout.String(), `"name": "steps"`) {
		t.Fatalf("explain pattern.steps --json: %v %s", err, stdout.String())
	}
}
