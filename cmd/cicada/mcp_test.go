package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/agentguide"
	"m31labs.dev/cicada/internal/mcpwire"
)

// TestMCPToolListBudget keeps the tool list small enough to sit in a model's
// context: names, descriptions and input schemas within 6,000 bytes (about
// 1,500 tokens), the whole list within 24,000, in a fixed order, and every
// description states the result cap.
func TestMCPToolListBudget(t *testing.T) {
	tools := mcpTools()
	brief, seen := 0, map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint {
			t.Errorf("tool %s: repeated or not marked read-only and closed-world", tool.Name)
		}
		if !strings.Contains(tool.Description, "256 KiB") || !strings.Contains(tool.Description, "too_large") {
			t.Errorf("tool %s: the description does not state the result cap", tool.Name)
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
	if strings.Join(names, " ") != "song check intents explain library params" {
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

// startMCPIn prepares the process as cicada mcp --root root does, and
// restores the working folder and CICADA_LIBRARY after the test.
func startMCPIn(t *testing.T, root string, allowUserLibrary bool) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)
	t.Setenv("CICADA_LIBRARY", os.Getenv("CICADA_LIBRARY"))
	cleanup, err := mcpStart(root, allowUserLibrary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
}

// commandJSON runs a command's function and decodes the JSON it printed.
func commandJSON(t *testing.T, label string, run func(w io.Writer) error) any {
	t.Helper()
	var stdout bytes.Buffer
	_ = run(&stdout)
	var document any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("%s printed no JSON: %v %q", label, err, stdout.String())
	}
	return document
}

// TestMCPToolsReturnTheCommandDocuments calls every tool, refusals included,
// in a new project and requires, by value, the document its command prints in
// the same folder, and each resource to be the text its command prints, so
// the two cannot drift.
func TestMCPToolsReturnTheCommandDocuments(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	startMCPIn(t, filepath.Join(root, "demo"), false)
	if err := os.WriteFile("bad.cicada", []byte("cicada 2\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = rif }\nsong { main }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(args ...string) func(io.Writer) error {
		return func(w io.Writer) error { return checkCommand(args, w, io.Discard) }
	}
	params := func(args ...string) func(io.Writer) error {
		return func(w io.Writer) error { return paramsCommand(args, w) }
	}
	for _, tc := range []struct {
		tool, arguments string
		isError         bool
		command         func(io.Writer) error
	}{
		{"song", `{}`, false, func(w io.Writer) error { return songCommand([]string{"--json"}, w) }},
		{"song", `{"score":"main.cicada"}`, false, func(w io.Writer) error { return songCommand([]string{"--json", "main.cicada"}, w) }},
		{"song", `{"score":"main.cicada","include":["source","symbols","structure"]}`, false, func(w io.Writer) error {
			return songCommand([]string{"--json", "--include", "source,symbols,structure", "main.cicada"}, w)
		}},
		{"song", `{"score":"missing.cicada"}`, true, func(w io.Writer) error { return songCommand([]string{"--json", "missing.cicada"}, w) }},
		{"check", `{}`, true, check("--json")},
		{"check", `{"score":"main.cicada"}`, false, check("--json", "main.cicada")},
		{"check", `{"score":"main.cicada","profile":"browser"}`, false, check("--json", "--profile", "browser", "main.cicada")},
		{"check", `{"score":"bad.cicada"}`, true, check("--json", "bad.cicada")},
		{"intents", `{}`, false, func(w io.Writer) error { return intentsCommand([]string{"--json", "--index"}, w) }},
		{"intents", `{"kinds":["setparam"]}`, false, func(w io.Writer) error { return intentsCommand([]string{"--json", "setparam"}, w) }},
		{"intents", `{"kinds":["nosuch"]}`, true, func(w io.Writer) error { return intentsCommand([]string{"--json", "nosuch"}, w) }},
		{"explain", `{"diagnostic":"CICADA-PARAM"}`, false, func(w io.Writer) error { return diagnosticsCommand([]string{"--json", "CICADA-PARAM"}, w) }},
		{"explain", `{"diagnostic":"param"}`, false, func(w io.Writer) error { return diagnosticsCommand([]string{"--json", "param"}, w) }},
		{"explain", `{"diagnostic":"CICADA-NOSUCH"}`, true, func(w io.Writer) error { return diagnosticsCommand([]string{"--json", "CICADA-NOSUCH"}, w) }},
		{"explain", `{"construct":"pattern"}`, false, func(w io.Writer) error { return explain(w, "pattern", true) }},
		{"explain", `{"construct":"track.mixer"}`, false, func(w io.Writer) error { return explain(w, "track.mixer", true) }},
		{"library", `{}`, false, func(w io.Writer) error { return libCommand([]string{"list", "--json"}, w) }},
		{"library", `{"path":"std/drums"}`, false, func(w io.Writer) error { return libCommand([]string{"show", "std/drums", "--json"}, w) }},
		{"library", `{"path":"nosuch/lib"}`, true, func(w io.Writer) error { return libCommand([]string{"show", "nosuch/lib", "--json"}, w) }},
		{"params", `{}`, false, params("--json", "--index")},
		{"params", `{"voice":"fm_bass"}`, false, params("--json", "--voice", "fm_bass")},
		{"params", `{"voice":"fm","scope":"patch","control":"fm_ep.op2"}`, false, params("--json", "--voice", "fm", "--scope", "patch", "fm_ep.op2")},
		{"params", `{"scope":"effect"}`, false, params("--json", "--scope", "effect")},
		{"params", `{"voice":"delay"}`, true, params("--json", "--voice", "delay")},
		{"params", `{"scope":"nosuch"}`, true, params("--json", "--scope", "nosuch")},
	} {
		document, isError := mcpDocument(t, tc.tool, tc.arguments)
		want := commandJSON(t, tc.tool+" "+tc.arguments, tc.command)
		if isError != tc.isError || !bytes.Equal(mustMarshal(document), mustMarshal(want)) {
			t.Errorf("%s %s (isError %v) differs from its command:\n%s\n%s", tc.tool, tc.arguments, isError, mustMarshal(document), mustMarshal(want))
		}
	}

	// intents with several kinds is the intents of each kind, in order, in one
	// document.
	document, isError := mcpDocument(t, "intents", `{"kinds":["setparam","togglestep"]}`)
	first := commandJSON(t, "intents setparam", func(w io.Writer) error { return intentsCommand([]string{"--json", "setparam"}, w) }).(map[string]any)
	second := commandJSON(t, "intents togglestep", func(w io.Writer) error { return intentsCommand([]string{"--json", "togglestep"}, w) }).(map[string]any)
	first["intents"] = append(first["intents"].([]any), second["intents"].([]any)...)
	if isError || !bytes.Equal(mustMarshal(document), mustMarshal(first)) {
		t.Errorf("intents of two kinds differs from the two documents of intents --json:\n%s", mustMarshal(document))
	}

	// params without arguments is the index; with a filter, the controls.
	if document, isError := mcpDocument(t, "params", `{}`); isError || document["schema"] != paramsIndexSchema {
		t.Errorf("params without arguments: %v %v", isError, document["schema"])
	}
	if document, isError := mcpDocument(t, "params", `{"voice":"fm_bass"}`); isError || document["schema"] != paramsSchema {
		t.Errorf("params with a voice: %v %v", isError, document["schema"])
	}

	// Each resource is the text its command prints.
	for _, tc := range []struct {
		uri  string
		args []string
	}{{mcpQuickStartURI, nil}, {mcpReferenceURI, []string{"reference"}}} {
		contents, err := mcpRead(tc.uri)
		var stdout bytes.Buffer
		if code := agentsCommand(tc.args, &stdout, io.Discard); err != nil || code != 0 || contents.Text != stdout.String() || contents.URI != tc.uri || contents.MimeType != "text/markdown" {
			t.Errorf("resource %s differs from cicada agents %v: %v", tc.uri, tc.args, err)
		}
	}
	listed := mcpResources()
	if len(listed) != 2 || listed[0].URI != mcpQuickStartURI || listed[0].Size != len(agentguide.QuickStart) || listed[1].Size != len(agentsReference()) {
		t.Errorf("resources/list: %+v", listed)
	}
	if _, err := mcpRead("cicada://guide/nosuch"); !errors.Is(err, mcpwire.ErrResourceNotFound) {
		t.Errorf("an unknown resource: %v", err)
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
	if document, isError := mcpDocument(t, "params", `{"filter":"mix"}`); !isError || documentError(document) != "usage" {
		t.Fatalf("params with an argument: %v %v", isError, document)
	}
	var toolErr *mcpwire.ToolError
	if _, err := mcpCall("nosuch", json.RawMessage(`{}`)); !errors.As(err, &toolErr) || toolErr.Code != mcpwire.CodeInvalidParams {
		t.Fatalf("an unknown tool: %v", err)
	}
	if _, err := mcpCall("check", json.RawMessage(`[]`)); !errors.As(err, &toolErr) {
		t.Fatalf("arguments that are not an object: %v", err)
	}
}

// TestMCPRefusesFlagShapedArguments: no value an agent sends is read as a
// command-line flag, and intents describes at most eight kinds.
func TestMCPRefusesFlagShapedArguments(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	startMCPIn(t, filepath.Join(root, "demo"), false)
	for _, tc := range []struct{ tool, arguments, schema string }{
		{"intents", `{"kinds":["--json"]}`, intentsSchema},
		{"intents", `{"kinds":["--index"]}`, intentsSchema},
		{"intents", `{"kinds":["setparam","--json"]}`, intentsSchema},
		{"intents", `{"kinds":["a","b","c","d","e","f","g","h","i"]}`, intentsSchema},
		{"explain", `{"diagnostic":"--json"}`, explainSchema},
		{"explain", `{"construct":"-x"}`, explainSchema},
		{"library", `{"path":"--json"}`, librarySchema},
		{"song", `{"score":"main.cicada","include":["-x"]}`, songSchema},
		{"check", `{"score":"--json"}`, checkSchema},
	} {
		if document, isError := mcpDocument(t, tc.tool, tc.arguments); !isError || documentError(document) != "usage" || document["schema"] != tc.schema {
			t.Errorf("%s %s: %v %v", tc.tool, tc.arguments, isError, document)
		}
	}
}

// TestMCPResultCap refuses a result over 256 KiB with too_large, in the
// tool's own format, with a hint on narrowing the request.
func TestMCPResultCap(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	startMCPIn(t, filepath.Join(root, "demo"), false)
	if err := os.WriteFile("big.cicada", bytes.Repeat([]byte("// a long comment line\n"), 300<<10/23), 0o644); err != nil {
		t.Fatal(err)
	}
	document, isError := mcpDocument(t, "song", `{"score":"big.cicada"}`)
	failure, _ := document["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if !isError || document["schema"] != songSchema || documentError(document) != "too_large" || !strings.Contains(message, "262144") || !strings.Contains(message, "ask for one score") {
		t.Fatalf("a result over the cap: %v %v", isError, document)
	}
	var eight []string
	for _, info := range edits.Catalog()[:mcpMaxKinds] {
		eight = append(eight, info.Kind)
	}
	if document, isError := mcpDocument(t, "intents", string(mustMarshal(map[string]any{"kinds": eight}))); isError {
		t.Fatalf("eight kinds fit under the cap: %v", document)
	}
}

// TestMCPRootConfinement: the root must be a project's own folder, and no tool
// reads or names a path outside it.
func TestMCPRootConfinement(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	userLibrary := t.TempDir()
	t.Setenv("CICADA_LIBRARY", userLibrary)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.cicada"), []byte("cicada 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "demo", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := libNewCommand([]string{"demo/tone"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := newProject("linked", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked", "lib")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ root, want string }{
		{filepath.Join(root, "demo", "sub"), "inside the project"},
		{filepath.Join(userLibrary, "demo", "tone"), "is the library"},
		{filepath.Join(root, "linked"), "lib leaves the root through a symbolic link"},
		{filepath.Join(root, "nosuch"), "nosuch"},
	} {
		t.Chdir(root)
		cleanup, err := mcpStart(tc.root, false)
		if err == nil {
			cleanup()
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("mcpStart(%s): %v, want %q", tc.root, err, tc.want)
		}
	}

	t.Chdir(root)
	startMCPIn(t, filepath.Join(root, "demo"), false)
	if err := os.Symlink(filepath.Join(outside, "secret.cicada"), "escape.cicada"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, "away"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ tool, arguments, code string }{
		{"song", `{"score":"escape.cicada"}`, "path_outside_root"},
		{"check", `{"score":"escape.cicada"}`, "path_outside_root"},
		{"song", `{"score":"away/secret.cicada"}`, "path_outside_root"},
		{"library", `{"path":"../outside"}`, "unknown_library"},
		{"library", `{"path":"demo/tone"}`, "unknown_library"},
	} {
		if document, isError := mcpDocument(t, tc.tool, tc.arguments); !isError || documentError(document) != tc.code {
			t.Errorf("%s %s: %v %v", tc.tool, tc.arguments, isError, document)
		}
	}
	// Every path a result names is relative to the root, with forward slashes.
	for _, arguments := range []string{`{}`, `{"score":"main.cicada"}`} {
		for _, tool := range []string{"song", "check"} {
			document, _ := mcpDocument(t, tool, arguments)
			text := string(mustMarshal(document))
			if strings.Contains(text, root) || strings.Contains(text, outside) || strings.Contains(text, `\\`) || strings.Contains(text, "secret") {
				t.Errorf("%s %s names a path outside the root: %s", tool, arguments, text)
			}
		}
	}
}

// TestMCPUserLibraryIsOffByDefault: the server reads the user library only
// with --allow-user-library, and then does not name its folder.
func TestMCPUserLibraryIsOffByDefault(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	userLibrary := t.TempDir()
	t.Setenv("CICADA_LIBRARY", userLibrary)
	if err := libNewCommand([]string{"demo/tone"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	listed := func() string {
		document, isError := mcpDocument(t, "library", `{}`)
		if isError {
			t.Fatalf("library: %v", document)
		}
		return string(mustMarshal(document))
	}

	startMCPIn(t, filepath.Join(root, "demo"), false)
	stand := os.Getenv("CICADA_LIBRARY")
	if stand == userLibrary || stand == "" {
		t.Fatalf("CICADA_LIBRARY is %q; the server must point it at an empty folder", stand)
	}
	if text := listed(); strings.Contains(text, "demo/tone") || !strings.Contains(text, "std/drums") {
		t.Fatalf("the user library was read by default: %s", text)
	}
	if document, isError := mcpDocument(t, "library", `{"path":"demo/tone"}`); !isError || documentError(document) != "unknown_library" {
		t.Fatalf("library demo/tone by default: %v %v", isError, document)
	}

	t.Setenv("CICADA_LIBRARY", userLibrary)
	t.Chdir(root)
	cleanup, err := mcpStart(filepath.Join(root, "demo"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if text := listed(); !strings.Contains(text, `{"kind":"user","path":"demo/tone"}`) {
		t.Fatalf("--allow-user-library: %s", text)
	}
	document, isError := mcpDocument(t, "library", `{"path":"demo/tone"}`)
	shown, _ := document["library"].(map[string]any)
	if isError || shown["kind"] != "user" || shown["root"] != "" || shown["sha256"] == "" || strings.Contains(string(mustMarshal(document)), userLibrary) {
		t.Fatalf("library demo/tone with --allow-user-library: %v %v", isError, document)
	}
}

// TestMCPStartRemovesItsEmptyFolder: the stand-in for the user library is
// removed when the server stops.
func TestMCPStartRemovesItsEmptyFolder(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("CICADA_LIBRARY", "")
	cleanup, err := mcpStart(filepath.Join(root, "demo"), false)
	if err != nil {
		t.Fatal(err)
	}
	stand := os.Getenv("CICADA_LIBRARY")
	if entries, err := os.ReadDir(stand); err != nil || len(entries) != 0 {
		t.Fatalf("the stand-in %s: %v %v", stand, entries, err)
	}
	cleanup()
	if _, err := os.Stat(stand); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stand-in %s was not removed: %v", stand, err)
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
