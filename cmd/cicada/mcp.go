package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/internal/agentguide"
	"m31labs.dev/cicada/internal/mcpwire"
	"m31labs.dev/cicada/project"
)

const mcpUsage = "usage: cicada mcp [--root <dir>] [--allow-user-library]"

// mcpResultCap is the largest tool result, in bytes of compact JSON: 256 KiB,
// so one call cannot fill a model's context. A larger result is refused with
// too_large and a hint on how to narrow the request.
const mcpResultCap = 256 << 10

// The resources the server serves: the two guide pages the binary carries.
const (
	mcpQuickStartURI = "cicada://guide/quickstart"
	mcpReferenceURI  = "cicada://guide/reference"
)

// paramRegistrySchema names the format of the params tool: the registry
// cicada params prints, as the params array of one object, because a tool
// result is an object.
const paramRegistrySchema = "cicada.param_registry.v1"

// mcpCommand serves the Model Context Protocol on standard input and output
// with the read-only tools and the guide resources. --root (default: the
// working folder) is the one folder the tools read; see mcpStart. Standard
// output carries protocol messages only: the server points os.Stdout at
// standard error, so nothing printed elsewhere can corrupt the stream. It
// returns the exit status.
func mcpCommand(args []string, stdin io.Reader, stdout, stderr *os.File) int {
	root, allowUserLibrary := ".", false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--root" && i+1 < len(args):
			root = args[i+1]
			i++
		case args[i] == "--allow-user-library":
			allowUserLibrary = true
		default:
			fmt.Fprintln(stderr, mcpUsage)
			return 2
		}
	}
	cleanup, err := mcpStart(root, allowUserLibrary)
	if err != nil {
		fmt.Fprintln(stderr, "cicada mcp:", err)
		return 2
	}
	defer cleanup()
	os.Stdout = stderr
	server := &mcpwire.Server{Name: "cicada", Version: cicadaVersion(), Instructions: mcpInstructions, Tools: mcpTools(), Call: mcpCall, Resources: mcpResources(), Read: mcpRead, Log: stderr}
	if err := server.Serve(stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "cicada mcp:", err)
		return 1
	}
	return 0
}

// mcpStart makes root the working folder and the one folder the tools read.
// It refuses a root that is not a project's own folder: one inside a project
// whose cicada.mod lies above it, a library folder (whose sources belong to
// a project above it), or one whose cicada.mod or lib folder leaves it
// through a symbolic link. Unless allowUserLibrary is set, it points
// CICADA_LIBRARY at a new empty folder, so the user library outside the root
// is not read; cleanup removes that folder.
func mcpStart(root string, allowUserLibrary bool) (cleanup func(), err error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("--root %s: %w", root, err)
	}
	if resolved, err = filepath.Abs(resolved); err != nil {
		return nil, err
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("--root %s is not a folder", root)
	}
	if err := os.Chdir(resolved); err != nil {
		return nil, err
	}
	projectDir, err := projectRoot()
	if err != nil {
		return nil, fmt.Errorf("--root %s: %w", root, err)
	}
	if projectResolved, err := filepath.EvalSymlinks(projectDir); err != nil || projectResolved != resolved {
		return nil, fmt.Errorf("--root %s is inside the project at %s; start the server with --root %s, so the tools read only that project", root, projectDir, projectDir)
	}
	for _, name := range []string{"cicada.mod", "lib"} {
		target, err := filepath.EvalSymlinks(filepath.Join(resolved, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, inside := insideFolder(resolved, target); !inside {
			return nil, fmt.Errorf("--root %s: %s leaves the root through a symbolic link", root, name)
		}
	}
	if data, err := os.ReadFile(filepath.Join(resolved, "cicada.mod")); err == nil {
		manifest, err := edition.ParseProjectManifest(data)
		if err != nil {
			return nil, fmt.Errorf("--root %s: cicada.mod: %w", root, err)
		}
		if manifest.Library != "" {
			return nil, fmt.Errorf("--root %s is the library %s; start the server at the project that uses it", root, manifest.Library)
		}
	}
	if allowUserLibrary {
		return func() {}, nil
	}
	empty, err := os.MkdirTemp("", "cicada-mcp-no-user-library-")
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("CICADA_LIBRARY", empty); err != nil {
		_ = os.Remove(empty)
		return nil, err
	}
	return func() { _ = os.Remove(empty) }, nil
}

// cicadaVersion is the module version the binary was built from, or (devel).
func cicadaVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

const mcpInstructions = "Cicada turns a text score into audio. Read the resource " + mcpQuickStartURI + " first, and " + mcpReferenceURI + " when you need a command, edit intent or diagnostic code. Every tool returns one JSON document of the cicada command line (its schema field names the format, such as cicada.check.v1), in structuredContent and as text; isError is true when the command would exit with an error, and the document's error.code says why. Paths are relative to the server's root. A result over 256 KiB is refused with too_large."

// mcpCapNote ends every tool's description.
const mcpCapNote = " A result over 256 KiB is refused with too_large."

// mcpTool is one tool: what it lists, how it runs, the format of its
// refusals, and how to narrow a request whose result is too large.
type mcpTool struct {
	tool   mcpwire.Tool
	run    func(arguments json.RawMessage) (any, bool)
	schema string
	narrow string
}

func readOnly() *mcpwire.Annotations {
	return &mcpwire.Annotations{ReadOnlyHint: true, IdempotentHint: true}
}

// outputSchema states the formats a tool returns, by their schema strings.
func outputSchema(schemas ...string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"schema": map[string]any{"type": "string", "enum": schemas}},
		"required":   []string{"schema"},
	})
	return data
}

var mcpScoreProperty = map[string]any{"type": "string", "description": "a score, relative to the root, ending in .cicada"}

func inputSchema(properties map[string]any) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "additionalProperties": false})
	return data
}

// mcpToolTable lists the tools in the order tools/list gives them.
func mcpToolTable() []mcpTool {
	return []mcpTool{
		{mcpwire.Tool{Name: "song", Title: "Read a score", Description: "Without score: every score of the project with its revision. With score: its revision and, as include asks, its source (default), the names it defines (symbols) and its compiled meaning (structure). The document is cicada.song.v1, as cicada song --json prints it." + mcpCapNote,
			InputSchema: inputSchema(map[string]any{"score": mcpScoreProperty, "include": map[string]any{"type": "array", "items": map[string]any{"enum": songIncludes}}}), OutputSchema: outputSchema(songSchema), Annotations: readOnly()}, mcpSong, songSchema,
			"ask for one score, and include only what you need (symbols is the smallest)"},
		{mcpwire.Tool{Name: "check", Title: "Check a score", Description: "Validate one score, or the whole project without score: cicada.check.v1, with each diagnostic's code, position, hint, any fix as intents, and each score's revision. isError is true when a score has errors." + mcpCapNote,
			InputSchema: inputSchema(map[string]any{"score": mcpScoreProperty, "profile": map[string]any{"enum": []string{profileDesktop, profileBrowser}}}), OutputSchema: outputSchema(checkSchema), Annotations: readOnly()}, mcpCheck, checkSchema,
			"check one score at a time"},
		{mcpwire.Tool{Name: "intents", Title: "Describe edit intents", Description: "Without kinds: every edit intent kind with its one-line meaning (cicada.intents_index.v1). With kinds (up to 8): their fields, units, allowed values and examples that apply accepts (cicada.intents.v1)." + mcpCapNote,
			InputSchema: inputSchema(map[string]any{"kinds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": mcpMaxKinds}}), OutputSchema: outputSchema(intentsIndexSchema, intentsSchema), Annotations: readOnly()}, mcpIntents, intentsSchema,
			"ask for fewer kinds"},
		{mcpwire.Tool{Name: "explain", Title: "Explain a code or construct", Description: "Give one of: diagnostic, a code such as CICADA-PARAM (cicada.diagnostics.v1: its meaning and fix); or construct, a language construct or construct.field such as pattern or track.mixer (cicada.explain.v1)." + mcpCapNote,
			InputSchema: inputSchema(map[string]any{"diagnostic": map[string]any{"type": "string"}, "construct": map[string]any{"type": "string"}}), OutputSchema: outputSchema(diagnosticsSchema, explainSchema), Annotations: readOnly()}, mcpExplain, explainSchema,
			"ask for one field, such as track.mixer"},
		{mcpwire.Tool{Name: "library", Title: "List or show libraries", Description: "Without path: the libraries the project can import, each with where it resolves (cicada.library.v1). With path, such as std/drums: its manifest, declarations, audio files and content hash." + mcpCapNote,
			InputSchema: inputSchema(map[string]any{"path": map[string]any{"type": "string"}}), OutputSchema: outputSchema(librarySchema), Annotations: readOnly()}, mcpLibrary, librarySchema,
			"show one library at a time"},
		{mcpwire.Tool{Name: "params", Title: "List the sound parameters", Description: "The live-parameter registry: every control of a track, voice, effect or the mixer, with its path, unit, range, default and whether it can change while playing. The array cicada params prints, as params in cicada.param_registry.v1; about 73 KB." + mcpCapNote,
			InputSchema: inputSchema(map[string]any{}), OutputSchema: outputSchema(paramRegistrySchema), Annotations: readOnly()}, mcpParams, paramRegistrySchema,
			"run cicada params instead"},
	}
}

// mcpMaxKinds is the most intent kinds one intents call describes.
const mcpMaxKinds = 8

func mcpTools() []mcpwire.Tool {
	var tools []mcpwire.Tool
	for _, entry := range mcpToolTable() {
		tools = append(tools, entry.tool)
	}
	return tools
}

func mcpCall(name string, arguments json.RawMessage) (mcpwire.Result, error) {
	for _, entry := range mcpToolTable() {
		if entry.tool.Name != name {
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(arguments, &probe); err != nil || probe == nil {
			return mcpwire.Result{}, &mcpwire.ToolError{Code: mcpwire.CodeInvalidParams, Message: "arguments must be an object"}
		}
		document, isError := entry.run(arguments)
		data, err := json.Marshal(document)
		if err != nil {
			return mcpwire.Result{}, err
		}
		if len(data) > mcpResultCap {
			document, isError = mcpRefusal(entry.schema, "too_large", fmt.Errorf("the result is %d bytes, over the %d-byte limit of one tool result; %s", len(data), mcpResultCap, entry.narrow))
			if data, err = json.Marshal(document); err != nil {
				return mcpwire.Result{}, err
			}
		}
		return mcpwire.Result{Document: data, IsError: isError}, nil
	}
	return mcpwire.Result{}, &mcpwire.ToolError{Code: mcpwire.CodeInvalidParams, Message: "unknown tool: " + name}
}

// mcpArguments decodes a tool's arguments, refusing a field the tool does
// not take and any value that starts with "-", which no name or path a tool
// takes does, so nothing an agent sends can read as a flag.
func mcpArguments(arguments json.RawMessage, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var values map[string]any
	_ = json.Unmarshal(arguments, &values)
	for field, value := range values {
		texts, _ := value.([]any)
		if text, ok := value.(string); ok {
			texts = []any{text}
		}
		for _, text := range texts {
			if text, ok := text.(string); ok && strings.HasPrefix(text, "-") {
				return fmt.Errorf("%s: %q starts with -; give a name or a path, not a flag", field, text)
			}
		}
	}
	return nil
}

// mcpRefusal is the error document of a tool, in the format it returns.
func mcpRefusal(schema, code string, err error) (any, bool) {
	return jsonErrorDocument{Schema: schema, Error: jsonError{Code: code, Message: err.Error()}}, true
}

// mcpPath refuses a path that leaves the root: absolute, with .., or through
// a symbolic link out of it.
func mcpPath(name, suffix string) error {
	clean := path.Clean(filepath.ToSlash(name))
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") || clean == ".." || strings.HasPrefix(clean, "../") || suffix != "" && !strings.HasSuffix(clean, suffix) {
		return fmt.Errorf("%q is not a path inside the root ending in %s", name, suffix)
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Stat(clean); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%q is not inside the root: %w", name, err)
	}
	return nil
}

// jsonDocument returns what a --json command printed, as raw JSON.
func jsonDocument(output []byte) any {
	return json.RawMessage(bytes.TrimSpace(output))
}

func mcpSong(arguments json.RawMessage) (any, bool) {
	var in struct {
		Score   string   `json:"score"`
		Include []string `json:"include"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(songSchema, "usage", err)
	}
	if in.Score != "" {
		if err := mcpPath(in.Score, ".cicada"); err != nil {
			return mcpRefusal(songSchema, "path_outside_root", err)
		}
	}
	document, code, err := songJSON(songOptions{score: in.Score, include: in.Include})
	if err != nil {
		return mcpRefusal(songSchema, code, err)
	}
	return document, false
}

func mcpCheck(arguments json.RawMessage) (any, bool) {
	var in struct {
		Score   string `json:"score"`
		Profile string `json:"profile"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(checkSchema, "usage", err)
	}
	_, limit, err := parseMaxResidentPCM(nil)
	if err != nil {
		return mcpRefusal(checkSchema, "usage", err)
	}
	options := checkOptions{profile: profileDesktop, maxResidentPCM: limit, json: true}
	if in.Profile != "" {
		if in.Profile != profileDesktop && in.Profile != profileBrowser {
			return mcpRefusal(checkSchema, "usage", fmt.Errorf("unknown profile %q (valid: desktop, browser)", in.Profile))
		}
		options.profile = in.Profile
	}
	if in.Score != "" {
		if err := mcpPath(in.Score, ".cicada"); err != nil {
			return mcpRefusal(checkSchema, "path_outside_root", err)
		}
	}
	var stdout bytes.Buffer
	err = runCheck(options, in.Score, &stdout, io.Discard)
	return jsonDocument(stdout.Bytes()), err != nil
}

func mcpIntents(arguments json.RawMessage) (any, bool) {
	var in struct {
		Kinds []string `json:"kinds"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(intentsSchema, "usage", err)
	}
	if len(in.Kinds) > mcpMaxKinds {
		return mcpRefusal(intentsSchema, "usage", fmt.Errorf("at most %d kinds at once", mcpMaxKinds))
	}
	if len(in.Kinds) == 0 {
		return intentsIndex(), false
	}
	document, code, err := intentsFor(in.Kinds)
	if err != nil {
		return mcpRefusal(intentsSchema, code, err)
	}
	return document, false
}

func mcpExplain(arguments json.RawMessage) (any, bool) {
	var in struct {
		Diagnostic string `json:"diagnostic"`
		Construct  string `json:"construct"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(explainSchema, "usage", err)
	}
	switch {
	case in.Diagnostic != "" && in.Construct == "":
		document, code, err := diagnosticsFor(in.Diagnostic)
		if err != nil {
			return mcpRefusal(diagnosticsSchema, code, err)
		}
		return document, false
	case in.Construct != "" && in.Diagnostic == "":
		var stdout bytes.Buffer
		if err := explain(&stdout, in.Construct, true); err != nil {
			return mcpRefusal(explainSchema, "unknown_construct", err)
		}
		return jsonDocument(stdout.Bytes()), false
	}
	return mcpRefusal(explainSchema, "usage", errors.New("give exactly one of diagnostic and construct"))
}

func mcpLibrary(arguments json.RawMessage) (any, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(librarySchema, "usage", err)
	}
	document, code, err := libraryJSON(in.Path)
	if err != nil {
		document.Error = &jsonError{Code: code, Message: err.Error()}
		return document, true
	}
	// A user library (served only with --allow-user-library) resolves outside
	// the root; its folder is not named.
	if document.Library != nil && filepath.IsAbs(filepath.FromSlash(document.Library.Root)) {
		document.Library.Root = ""
	}
	return document, false
}

// paramRegistryDocument is the params tool's result: the registry cicada
// params prints, unchanged, in params.
type paramRegistryDocument struct {
	Schema string          `json:"schema"`
	Params json.RawMessage `json:"params"`
}

func mcpParams(arguments json.RawMessage) (any, bool) {
	var in struct{}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(paramRegistrySchema, "usage", err)
	}
	return paramRegistryDocument{Schema: paramRegistrySchema, Params: json.RawMessage(project.ParamsJSON())}, false
}

// mcpResources lists the guide pages: the same text cicada agents and cicada
// agents reference print.
func mcpResources() []mcpwire.Resource {
	return []mcpwire.Resource{
		{URI: mcpQuickStartURI, Name: "quickstart", Title: "Cicada quick start for agents", Description: "Read first: the loop, a song, the edit envelope, refusals and the listening report. What cicada agents prints.", MimeType: "text/markdown", Size: len(agentguide.QuickStart)},
		{URI: mcpReferenceURI, Name: "reference", Title: "Cicada core reference for agents", Description: "The quick start, the JSON conventions, and one line for every command, edit intent, diagnostic code and effect route. What cicada agents reference prints.", MimeType: "text/markdown", Size: len(agentsReference())},
	}
}

func mcpRead(uri string) (mcpwire.ResourceContents, error) {
	switch uri {
	case mcpQuickStartURI:
		return mcpwire.ResourceContents{URI: uri, MimeType: "text/markdown", Text: agentguide.QuickStart}, nil
	case mcpReferenceURI:
		return mcpwire.ResourceContents{URI: uri, MimeType: "text/markdown", Text: agentsReference()}, nil
	}
	return mcpwire.ResourceContents{}, mcpwire.ErrResourceNotFound
}
