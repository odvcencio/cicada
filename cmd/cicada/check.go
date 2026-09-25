package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type scoreInspection struct {
	source      []byte
	score       *notation.Score
	semantic    *project.Project
	diagnostics []notation.Diagnostic
}

// inspectScore is the validation gate shared by per-file commands and the
// project checker. It includes semantic conversion and engine compilation.
func inspectScore(path string) (scoreInspection, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return scoreInspection{}, err
	}
	if err := checkScoreEdition(path); err != nil {
		return scoreInspection{}, err
	}
	score, diagnostics := notation.Parse(source)
	var semantic *project.Project
	if !hasDiagnosticErrors(diagnostics) {
		var projectDiagnostics []notation.Diagnostic
		semantic, projectDiagnostics = project.FromScore(score)
		diagnostics = appendUniqueDiagnostics(diagnostics, projectDiagnostics)
		if semantic != nil {
			if _, err := project.CompileEngine(semantic, 48_000, 128); err != nil {
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: "CICADA-PARAM", Severity: "error", Message: err.Error(),
					Position: notation.Position{Line: 1, Column: 1},
				})
			}
		}
	}
	sort.SliceStable(diagnostics, func(i, j int) bool {
		a, b := diagnostics[i], diagnostics[j]
		if a.Position.Line != b.Position.Line {
			return a.Position.Line < b.Position.Line
		}
		if a.Position.Column != b.Position.Column {
			return a.Position.Column < b.Position.Column
		}
		if a.Severity != b.Severity {
			return a.Severity == "error"
		}
		return a.Code < b.Code
	})
	return scoreInspection{source: source, score: score, semantic: semantic, diagnostics: diagnostics}, nil
}

func checkCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) > 1 || len(args) == 1 && filepath.Ext(args[0]) != ".cicada" {
		return fmt.Errorf("usage: cicada check [score.cicada]")
	}
	if len(args) == 1 {
		return checkPaths([]string{args[0]}, stdout, stderr)
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	paths, err := projectScorePaths(root)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no .cicada scores found in %s", root)
	}
	return checkPaths(paths, stdout, stderr)
}

func checkPaths(paths []string, stdout, stderr io.Writer) error {
	failed := 0
	for _, path := range paths {
		inspection, err := inspectScore(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			failed++
			continue
		}
		for _, diagnostic := range inspection.diagnostics {
			printCheckDiagnostic(stderr, path, inspection.source, diagnostic)
			if diagnostic.Severity == "error" {
				failed++
			}
		}
		if !hasDiagnosticErrors(inspection.diagnostics) {
			fmt.Fprintln(stdout, path)
		}
	}
	if failed != 0 {
		return fmt.Errorf("%d error(s) across %d score(s)", failed, len(paths))
	}
	return nil
}

func printCheckDiagnostic(w io.Writer, path string, source []byte, d notation.Diagnostic) {
	fmt.Fprintf(w, "%s:%d:%d: %s %s: %s\n", path, d.Position.Line, d.Position.Column, d.Severity, d.Code, d.Message)
	lines := strings.Split(string(source), "\n")
	if d.Position.Line < 1 || d.Position.Line > len(lines) {
		return
	}
	line := lines[d.Position.Line-1]
	var prefix strings.Builder
	runes := []rune(line)
	for i := 0; i < d.Position.Column-1; i++ {
		if i < len(runes) && runes[i] == '\t' {
			prefix.WriteByte('\t')
		} else {
			prefix.WriteByte(' ')
		}
	}
	fmt.Fprintf(w, "  %s\n  %s^\n", line, prefix.String())
	if suggestion := checkSuggestion(d); suggestion != "" {
		fmt.Fprintf(w, "  %s\n", suggestion)
	}
}

func checkSuggestion(d notation.Diagnostic) string {
	switch {
	case d.Code == "CICADA-SCALE-DEGREE":
		return "choose a degree in the score's scale or use an absolute letter pitch"
	case d.Code == "CICADA-REFERENCE" && strings.Contains(d.Message, "unknown pattern"):
		return "use a declared pattern name in this scene"
	case d.Code == "CICADA-PARAM" && strings.Contains(d.Message, "steps attribute must equal"):
		return "remove steps= to infer the length from the cells"
	}
	return ""
}
