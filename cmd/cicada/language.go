package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/notation"
)

// languageArgs splits a command's flags from its one source path, accepting
// flags before or after the path as fmt does.
func languageArgs(args []string, allowed ...string) (string, map[string]bool) {
	flags := make(map[string]bool)
	var paths []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			paths = append(paths, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		known := false
		for _, flag := range allowed {
			known = known || flag == name
		}
		if !known {
			usage()
		}
		flags[name] = true
	}
	if len(paths) != 1 {
		usage()
	}
	return paths[0], flags
}

func highlightCommand(args []string) {
	path, flags := languageArgs(args, "html", "spans")
	if flags["html"] && flags["spans"] {
		usage()
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	spans, err := language.Highlight(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch {
	case flags["spans"]:
		err = language.WriteSpans(os.Stdout, src, spans)
	case flags["html"]:
		err = language.WriteHTML(os.Stdout, src, spans, language.Night, filepath.Base(path))
	case os.Getenv("NO_COLOR") != "":
		_, err = os.Stdout.Write(src)
	default:
		err = language.WriteANSI(os.Stdout, src, spans, language.Night)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if reportSyntaxErrors(path, src) {
		os.Exit(1)
	}
}

func symbolsCommand(args []string) {
	path, flags := languageArgs(args, "refs", "json")
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	symbols, err := language.Symbols(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	listed := symbols[:0]
	for _, symbol := range symbols {
		if flags["refs"] || symbol.Role == "definition" {
			listed = append(listed, symbol)
		}
	}
	if flags["json"] {
		writeJSON(listed)
	} else {
		for _, symbol := range listed {
			at := fmt.Sprintf("%d:%d", symbol.Position.Line, symbol.Position.Column)
			fmt.Printf("%-8s %-10s %-10s %s\n", at, symbol.Role, symbol.Kind, symbol.Name)
		}
	}
	if reportSyntaxErrors(path, src) {
		os.Exit(1)
	}
}

// reportSyntaxErrors prints syntax diagnostics only. The language tools
// work on scores that do not yet validate, so semantic checks stay with
// cicada validate.
func reportSyntaxErrors(path string, src []byte) bool {
	_, diagnostics := notation.Parse(src)
	found := false
	for _, d := range diagnostics {
		if d.Code == "CICADA-SYNTAX" {
			fmt.Fprintf(os.Stderr, "%s:%d:%d: %s %s: %s\n", path, d.Position.Line, d.Position.Column, d.Severity, d.Code, d.Message)
			found = true
		}
	}
	return found
}
