package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type scoreInspection struct {
	source      []byte
	sources     *project.Sources
	score       *notation.Score
	semantic    *project.Project
	diagnostics []notation.Diagnostic
}

// inspectScore is the validation gate shared by per-file commands and the
// project checker. It includes semantic conversion and engine compilation.
func inspectScore(path string) (scoreInspection, error) {
	sources, err := project.ReadSources(path, nil)
	if err != nil {
		return scoreInspection{}, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return scoreInspection{}, err
	}
	var source []byte
	for _, file := range sources.Files {
		if file.Path == absolute || file.Path == path {
			source = file.Source
			break
		}
	}
	score, diagnostics := sources.Parse()
	var semantic *project.Project
	if !hasDiagnosticErrors(diagnostics) {
		var projectDiagnostics []notation.Diagnostic
		semantic, projectDiagnostics = project.FromScore(score)
		diagnostics = appendUniqueDiagnostics(diagnostics, projectDiagnostics)
		if semantic != nil && !semantic.NeedsSampleEngine() {
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
	return scoreInspection{source: source, sources: sources, score: score, semantic: semantic, diagnostics: diagnostics}, nil
}

// parseCheckArgs reads the optional profile and score path. The score may come
// before or after the flags.
func parseCheckArgs(args []string) (profile, path string, err error) {
	errUsage := fmt.Errorf("usage: cicada check [--profile desktop|browser] [score.cicada]")
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("profile", profileDesktop, "limits to check: desktop or browser")
	var scores []string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		scores, rest = []string{rest[0]}, rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return "", "", errUsage
	}
	scores = append(scores, flags.Args()...)
	if len(scores) > 1 || len(scores) == 1 && filepath.Ext(scores[0]) != ".cicada" {
		return "", "", errUsage
	}
	if *name != profileDesktop && *name != profileBrowser {
		return "", "", fmt.Errorf("unknown profile %q (valid: desktop, browser)", *name)
	}
	if len(scores) == 1 {
		path = scores[0]
	}
	return *name, path, nil
}

func checkCommand(args []string, stdout, stderr io.Writer) error {
	profile, path, err := parseCheckArgs(args)
	if err != nil {
		return err
	}
	if path != "" {
		return checkPathsProfile([]string{path}, profile, stdout, stderr)
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
	if data, err := os.ReadFile(filepath.Join(root, "cicada.mod")); err == nil {
		manifest, err := edition.ParseProjectManifest(data)
		if err != nil {
			return err
		}
		if manifest.ExplicitSources() {
			paths = paths[:1]
		}
	}
	return checkPathsProfile(paths, profile, stdout, stderr)
}

func checkPaths(paths []string, stdout, stderr io.Writer) error {
	return checkPathsProfile(paths, profileDesktop, stdout, stderr)
}

func checkPathsProfile(paths []string, profile string, stdout, stderr io.Writer) error {
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
		if hasDiagnosticErrors(inspection.diagnostics) {
			continue
		}
		// Check loads the sampler packs, which inspectScore leaves to render.
		packDiagnostics := checkSamplerPacks(path, inspection)
		for _, diagnostic := range packDiagnostics {
			printCheckDiagnostic(stderr, path, inspection.source, diagnostic)
			if diagnostic.Severity == "error" {
				failed++
			}
		}
		if hasDiagnosticErrors(packDiagnostics) {
			continue
		}
		if profile == profileBrowser {
			if !checkBrowserScore(path, inspection, stdout, stderr) {
				failed++
			}
			continue
		}
		fmt.Fprintln(stdout, path)
	}
	if failed != 0 {
		return fmt.Errorf("%d error(s) across %d score(s)", failed, len(paths))
	}
	return nil
}

func printCheckDiagnostic(w io.Writer, path string, source []byte, d notation.Diagnostic) {
	if d.Position.File != "" && d.Position.File != path {
		path = d.Position.File
		if data, err := os.ReadFile(path); err == nil {
			source = data
		}
	}
	d.Position.File = path
	fmt.Fprintln(w, d.Error())
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
	case d.Code == samplerZoneCode:
		return "move the note into the pack's key range, change the sampler's root or the pattern's transpose, or use a pack that covers it"
	case d.Code == "CICADA-REFERENCE" && strings.Contains(d.Message, "unknown pattern"):
		return "use a declared pattern name in this scene"
	case d.Code == "CICADA-PARAM" && strings.Contains(d.Message, "steps attribute must equal"):
		return "remove steps= to infer the length from the cells"
	case d.Code == "CICADA-LIMIT" && strings.Contains(d.Message, "allocated voices"):
		return "remove tracks or lower voices on the tracks listed below"
	case d.Code == "CICADA-LIMIT" && strings.Contains(d.Message, "master processor"):
		return "a browser image cannot carry a master insert; put a compressor on bus music, or move other effects to track inserts (a track insert processes only its own track)"
	case d.Code == "CICADA-LIMIT" && strings.Contains(d.Message, imageTooLarge):
		return "shorten or remove audio assets and samples; the image stores their audio at 4 bytes per sample per channel"
	}
	return ""
}
