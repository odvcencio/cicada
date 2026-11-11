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

	"m31labs.dev/cicada/internal/mcpwire"
)

const mcpUsage = "usage: cicada mcp [--root <dir>]"

// mcpCommand serves the Model Context Protocol on standard input and output
// with the read-only tools. --root (default: the working folder) is the one
// folder the tools read; the server works from it, so paths in its documents
// are relative to it. Standard output carries protocol messages only: the
// server points os.Stdout at standard error, so nothing printed elsewhere can
// corrupt the stream. It returns the exit status.
func mcpCommand(args []string, stdin io.Reader, stdout, stderr *os.File) int {
	root := "."
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			root = args[i+1]
			i++
			continue
		}
		fmt.Fprintln(stderr, mcpUsage)
		return 2
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(stderr, "cicada mcp:", err)
		return 2
	}
	os.Stdout = stderr
	server := &mcpwire.Server{Name: "cicada", Version: cicadaVersion(), Instructions: mcpInstructions, Tools: mcpTools(), Call: mcpCall}
	if err := server.Serve(stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "cicada mcp:", err)
		return 1
	}
	return 0
}

// cicadaVersion is the module version the binary was built from, or (devel).
func cicadaVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

const mcpInstructions = "Cicada turns a text score into audio. Every tool returns one JSON document of the cicada command line (its schema field names the format, such as cicada.check.v1), in structuredContent and as text; isError is true when the command would exit with an error, and the document's error.code says why. Paths are relative to the server's root and end in .cicada. Read the quick start with the cicada agents command, or the core reference with cicada agents reference."

// mcpTool is one tool: what it lists and how it runs.
type mcpTool struct {
	tool mcpwire.Tool
	run  func(arguments json.RawMessage) (any, bool)
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
		{mcpwire.Tool{Name: "song", Title: "Read a score", Description: "Without score: every score of the project with its revision. With score: its revision and, as include asks, its source (default), the names it defines (symbols) and its compiled meaning (structure). The document is cicada.song.v1, as cicada song --json prints it.",
			InputSchema: inputSchema(map[string]any{"score": mcpScoreProperty, "include": map[string]any{"type": "array", "items": map[string]any{"enum": songIncludes}}}), OutputSchema: outputSchema(songSchema), Annotations: readOnly()}, mcpSong},
		{mcpwire.Tool{Name: "check", Title: "Check a score", Description: "Validate one score, or the whole project without score: cicada.check.v1, with each diagnostic's code, position, hint, any fix as intents, and each score's revision. isError is true when a score has errors.",
			InputSchema: inputSchema(map[string]any{"score": mcpScoreProperty, "profile": map[string]any{"enum": []string{profileDesktop, profileBrowser}}}), OutputSchema: outputSchema(checkSchema), Annotations: readOnly()}, mcpCheck},
		{mcpwire.Tool{Name: "intents", Title: "Describe edit intents", Description: "Without kinds: every edit intent kind with its one-line meaning (cicada.intents_index.v1). With kinds (up to 8): their fields, units, allowed values and examples that apply accepts (cicada.intents.v1).",
			InputSchema: inputSchema(map[string]any{"kinds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 8}}), OutputSchema: outputSchema(intentsIndexSchema, intentsSchema), Annotations: readOnly()}, mcpIntents},
		{mcpwire.Tool{Name: "explain", Title: "Explain a code or construct", Description: "Give one of: diagnostic, a code such as CICADA-PARAM (cicada.diagnostics.v1: its meaning and fix); or construct, a language construct or construct.field such as pattern or track.mixer (cicada.explain.v1).",
			InputSchema: inputSchema(map[string]any{"diagnostic": map[string]any{"type": "string"}, "construct": map[string]any{"type": "string"}}), OutputSchema: outputSchema(diagnosticsSchema, explainSchema), Annotations: readOnly()}, mcpExplain},
		{mcpwire.Tool{Name: "library", Title: "List or show libraries", Description: "Without path: the libraries the project can import, each with where it resolves (cicada.library.v1). With path, such as std/drums: its manifest, declarations, audio files and content hash.",
			InputSchema: inputSchema(map[string]any{"path": map[string]any{"type": "string"}}), OutputSchema: outputSchema(librarySchema), Annotations: readOnly()}, mcpLibrary},
	}
}

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
		return mcpwire.Result{Document: data, IsError: isError}, nil
	}
	return mcpwire.Result{}, &mcpwire.ToolError{Code: mcpwire.CodeInvalidParams, Message: "unknown tool: " + name}
}

// mcpArguments decodes a tool's arguments, refusing a field the tool does
// not take.
func mcpArguments(arguments json.RawMessage, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
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
	args := []string{"--json"}
	if in.Profile != "" {
		args = append(args, "--profile", in.Profile)
	}
	if in.Score != "" {
		if err := mcpPath(in.Score, ".cicada"); err != nil {
			return mcpRefusal(checkSchema, "path_outside_root", err)
		}
		args = append(args, in.Score)
	}
	var stdout bytes.Buffer
	err := checkCommand(args, &stdout, io.Discard)
	return jsonDocument(stdout.Bytes()), err != nil
}

func mcpIntents(arguments json.RawMessage) (any, bool) {
	var in struct {
		Kinds []string `json:"kinds"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(intentsSchema, "usage", err)
	}
	if len(in.Kinds) > 8 {
		return mcpRefusal(intentsSchema, "usage", errors.New("at most 8 kinds at once"))
	}
	if len(in.Kinds) == 0 {
		var stdout bytes.Buffer
		err := intentsCommand([]string{"--json", "--index"}, &stdout)
		return jsonDocument(stdout.Bytes()), err != nil
	}
	// One kind at a time, as cicada intents --json <kind> gives it, then the
	// intents of all of them in one cicada.intents.v1 document.
	var merged intentsDocument
	for i, kind := range in.Kinds {
		var stdout bytes.Buffer
		if err := intentsCommand([]string{"--json", kind}, &stdout); err != nil {
			return jsonDocument(stdout.Bytes()), true
		}
		var one intentsDocument
		if err := json.Unmarshal(stdout.Bytes(), &one); err != nil {
			return mcpRefusal(intentsSchema, "internal", err)
		}
		if i == 0 {
			merged = one
			continue
		}
		merged.Intents = append(merged.Intents, one.Intents...)
	}
	return merged, false
}

func mcpExplain(arguments json.RawMessage) (any, bool) {
	var in struct {
		Diagnostic string `json:"diagnostic"`
		Construct  string `json:"construct"`
	}
	if err := mcpArguments(arguments, &in); err != nil {
		return mcpRefusal(explainSchema, "usage", err)
	}
	var stdout bytes.Buffer
	switch {
	case in.Diagnostic != "" && in.Construct == "":
		err := diagnosticsCommand([]string{"--json", in.Diagnostic}, &stdout)
		return jsonDocument(stdout.Bytes()), err != nil
	case in.Construct != "" && in.Diagnostic == "":
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
	return document, false
}
