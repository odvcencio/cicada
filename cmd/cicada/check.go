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

// root is the folder that the relative paths in the score resolve from: audio
// assets and sampler packs. It is the folder of the project's cicada.mod, or the
// score's own folder when there is none, whichever source file the command was
// given. path is the score as the command spelled it.
func (i scoreInspection) root(path string) string {
	if i.sources != nil && i.sources.Root != "" {
		return i.sources.Root
	}
	return filepath.Dir(path)
}

// inspectScore is the validation gate shared by per-file commands and the
// project checker. It includes semantic conversion and engine compilation.
func inspectScore(path string) (scoreInspection, error) {
	return inspectScoreWith(path, nil)
}

// inspectScoreWith inspects a score with some source files replaced by the
// given bytes, keyed by absolute path, as a candidate edit would leave them.
func inspectScoreWith(path string, overrides map[string][]byte) (scoreInspection, error) {
	sources, err := project.ReadSources(path, overrides)
	if err != nil {
		return scoreInspection{}, err
	}
	return inspectSources(path, sources)
}

// inspectScorePinned inspects a score as if its project manifest were at
// manifestPath, as a fix that writes a new cicada.mod would leave it.
func inspectScorePinned(path string, overrides map[string][]byte, manifestPath string) (scoreInspection, error) {
	sources, err := project.ReadSourcesPinned(path, overrides, project.Resolution{ManifestPath: manifestPath})
	if err != nil {
		return scoreInspection{}, err
	}
	return inspectSources(path, sources)
}

func inspectSources(path string, sources *project.Sources) (scoreInspection, error) {
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

// checkUsage is the usage line of the check command.
const checkUsage = "usage: cicada check [--profile desktop|browser] [--max-resident-pcm <size>] [--json] [score.cicada]"

// parseCheckArgs reads the optional profile, the optional cap on decoded
// sampler-pack PCM (zero selects the default), the JSON switch and the score
// path. The score may come before or after the flags.
func parseCheckArgs(args []string) (options checkOptions, path string, err error) {
	errUsage := fmt.Errorf("%s", checkUsage)
	args, limit, err := parseMaxResidentPCM(args)
	if err != nil {
		return checkOptions{}, "", err
	}
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("profile", profileDesktop, "limits to check: desktop or browser")
	asJSON := flags.Bool("json", false, "write the result as JSON")
	var scores []string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		scores, rest = []string{rest[0]}, rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return checkOptions{}, "", errUsage
	}
	scores = append(scores, flags.Args()...)
	if len(scores) > 1 || len(scores) == 1 && filepath.Ext(scores[0]) != ".cicada" {
		return checkOptions{}, "", errUsage
	}
	if *name != profileDesktop && *name != profileBrowser {
		return checkOptions{}, "", fmt.Errorf("unknown profile %q (valid: desktop, browser)", *name)
	}
	if len(scores) == 1 {
		path = scores[0]
	}
	return checkOptions{profile: *name, maxResidentPCM: limit, json: *asJSON}, path, nil
}

func checkCommand(args []string, stdout, stderr io.Writer) error {
	options, path, err := parseCheckArgs(args)
	if err != nil {
		if hasJSONFlag(args) {
			return checkFailureJSON(stdout, checkOptions{profile: profileDesktop}, "", "usage", err)
		}
		return err
	}
	return runCheck(options, path, stdout, stderr)
}

// runCheck checks one score, or the project of the working folder when path
// is empty, as cicada check does after reading its arguments.
func runCheck(options checkOptions, path string, stdout, stderr io.Writer) error {
	if path != "" {
		return checkPathsWith([]string{path}, options, stdout, stderr)
	}
	fail := func(code string, err error) error {
		if options.json {
			return checkFailureJSON(stdout, options, "", code, err)
		}
		return err
	}
	root, err := projectRoot()
	if err != nil {
		if options.json {
			return checkFailureJSON(stdout, options, nearestManifest(), "project_unreadable", err)
		}
		return err
	}
	paths, err := projectScorePaths(root)
	if err != nil {
		if options.json {
			return checkFailureJSON(stdout, options, filepath.Join(root, "cicada.mod"), "project_unreadable", err)
		}
		return err
	}
	if len(paths) == 0 {
		return fail("no_scores", fmt.Errorf("no .cicada scores found in %s", root))
	}
	if data, err := os.ReadFile(filepath.Join(root, "cicada.mod")); err == nil {
		manifest, err := edition.ParseProjectManifest(data)
		if err != nil {
			if options.json {
				return checkFailureJSON(stdout, options, filepath.Join(root, "cicada.mod"), "project_unreadable", err)
			}
			return err
		}
		if manifest.ExplicitSources() {
			paths = paths[:1]
		}
	}
	return checkPathsWith(paths, options, stdout, stderr)
}

// nearestManifest is the cicada.mod of the working folder or the closest
// folder above it, or "" when there is none.
func nearestManifest() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "cicada.mod")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// checkOptions are what a check run takes beyond the scores: the profile whose
// limits apply, the cap on decoded sampler-pack PCM, in bytes, where zero is
// the default, and whether to write JSON instead of text.
type checkOptions struct {
	profile        string
	maxResidentPCM int64
	json           bool
}

func checkPaths(paths []string, stdout, stderr io.Writer) error {
	return checkPathsProfile(paths, profileDesktop, stdout, stderr)
}

func checkPathsProfile(paths []string, profile string, stdout, stderr io.Writer) error {
	return checkPathsWith(paths, checkOptions{profile: profile}, stdout, stderr)
}

func checkPathsWith(paths []string, options checkOptions, stdout, stderr io.Writer) error {
	if options.json {
		return checkPathsJSON(paths, options, stdout)
	}
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
		// Check loads the sampler packs, which inspectScore leaves to render,
		// and checks what else render refuses.
		packDiagnostics, _ := checkBeyondValidator(path, inspection, options.maxResidentPCM)
		for _, diagnostic := range packDiagnostics {
			printCheckDiagnostic(stderr, path, inspection.source, diagnostic)
			if diagnostic.Severity == "error" {
				failed++
			}
		}
		if hasDiagnosticErrors(packDiagnostics) {
			continue
		}
		if options.profile == profileBrowser {
			failed += checkBrowserScore(path, inspection, stdout, stderr)
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
		return "move the note into the pack's key range, write it in another octave, change the pattern's transpose, or use a pack that covers it (the sampler's root sets the octave of a pitch written without one)"
	case d.Code == "CICADA-ASSET-FORMAT" && strings.Contains(d.Message, "the sample voice"):
		return "resample the asset to a rate in the range given (expected), then declare its new rate, frames and sha256"
	case d.Code == "CICADA-LIMIT" && strings.Contains(d.Message, "--from and --bars"):
		return "render the song in ranges with --from and --bars, such as --from 1 --bars 256 and then --from 257"
	case d.Code == samplerRangeCode:
		return "write the note in an octave inside the range, set the sampler's root nearer the note, or play it from another asset recorded nearer that pitch"
	case d.Code == samplerMemoryCode:
		return "use smaller packs, or give a larger cap with --max-resident-pcm or CICADA_MAX_RESIDENT_PCM, such as 2GiB"
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
