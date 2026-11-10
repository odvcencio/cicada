package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// marshalAgentJSON indents a --json document and leaves <, > and & as they
// are, so text such as pattern:<name> reads as written.
func marshalAgentJSON(v any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// writeAgentJSON writes one --json document and a newline.
func writeAgentJSON(w io.Writer, v any) error {
	data, err := marshalAgentJSON(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// jsonPath is how every --json document shows a path: relative to the
// working folder, with forward slashes, when the path is inside it, and
// absolute otherwise.
func jsonPath(path string) string {
	if path == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	wd, err := os.Getwd()
	if err != nil {
		return absolute
	}
	if relative, ok := insideFolder(wd, absolute); ok {
		return relative
	}
	// The same folder can have two spellings through a symbolic link, such as
	// /var and /private/var on macOS; compare the resolved ones too.
	resolvedWD, wdErr := filepath.EvalSymlinks(wd)
	resolvedDir, dirErr := filepath.EvalSymlinks(filepath.Dir(absolute))
	if wdErr == nil && dirErr == nil {
		if relative, ok := insideFolder(resolvedWD, filepath.Join(resolvedDir, filepath.Base(absolute))); ok {
			return relative
		}
	}
	return absolute
}

// insideFolder returns path relative to folder, with forward slashes, when
// path is inside it.
func insideFolder(folder, path string) (string, bool) {
	relative, err := filepath.Rel(folder, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

// jsonError is the error object of every --json document: a stable code and
// the message the command prints on standard error.
type jsonError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// jsonErrorDocument is the document a --json command prints when it fails
// before it has anything else to report, such as on a usage error.
type jsonErrorDocument struct {
	Schema string    `json:"schema"`
	Error  jsonError `json:"error"`
}

// hasJSONFlag reports whether the arguments ask for JSON.
func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			return true
		}
	}
	return false
}
