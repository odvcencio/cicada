package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// checkSchema names the format of cicada check --json.
const checkSchema = "cicada.check.v1"

type checkDocument struct {
	Schema   string             `json:"schema"`
	Profile  string             `json:"profile"`
	Valid    bool               `json:"valid"`
	Errors   int                `json:"errors"`
	Warnings int                `json:"warnings"`
	Scores   []checkScoreReport `json:"scores"`
	Error    *jsonError         `json:"error,omitempty"`
}

// checkFailureJSON writes the document of a check that could not check its
// scores: a usage error, or a project that cannot be read. A cicada.mod
// error with a diagnostic code is also listed as that file's diagnostic.
// It returns err, so the exit status stays that of the text command.
func checkFailureJSON(stdout io.Writer, options checkOptions, manifest, code string, err error) error {
	document := checkDocument{Schema: checkSchema, Profile: options.profile, Scores: []checkScoreReport{}, Error: &jsonError{Code: code, Message: err.Error()}}
	var manifestErr *edition.ManifestError
	var sourceErr *project.SourceError
	var diagnostic *notation.Diagnostic
	switch {
	case errors.As(err, &manifestErr) && manifest != "":
		diagnostic = &notation.Diagnostic{Code: manifestErr.Code, Severity: "error", Message: manifestErr.Message, Position: notation.Position{File: manifest, Line: manifestErr.Line, Column: max(manifestErr.Column, 1)}}
	case errors.As(err, &sourceErr):
		diagnostic = &sourceErr.Diagnostic
	}
	if diagnostic != nil {
		file := diagnostic.Position.File
		if file == "" {
			file = manifest
		}
		source, _ := os.ReadFile(file)
		document.Errors = 1
		document.Scores = append(document.Scores, checkScoreReport{File: jsonPath(file), Diagnostics: []checkDiagnosticReport{checkDiagnosticJSON(file, source, *diagnostic, nil)}})
	}
	if writeErr := writeAgentJSON(stdout, document); writeErr != nil {
		return writeErr
	}
	return err
}

// checkScoreReport is one score's result. Revision is the SHA-256 of the
// score's bytes in hex, the form cicada apply checks an envelope's revision
// against; it is empty only when the score cannot be read.
type checkScoreReport struct {
	File        string                  `json:"file"`
	Revision    string                  `json:"revision,omitempty"`
	Valid       bool                    `json:"valid"`
	Diagnostics []checkDiagnosticReport `json:"diagnostics"`
	Browser     *checkBrowserReport     `json:"browser_profile,omitempty"`
	Packs       []checkPackReport       `json:"packs,omitempty"`
}

// checkPosition is a place in a source file. Line and column count from 1;
// columns count Unicode characters. Offset is the byte offset from the start
// of the file, counted from 0.
type checkPosition struct {
	File   string `json:"file,omitempty"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Offset int    `json:"offset"`
}

type checkDiagnosticReport struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	File     string         `json:"file"`
	Start    *checkPosition `json:"start,omitempty"`
	End      *checkPosition `json:"end,omitempty"`
	Related  *checkPosition `json:"related,omitempty"`
	Hint     string         `json:"hint,omitempty"`
	Fix      *checkFix      `json:"fix,omitempty"`
}

// checkFix is a repair the checker knows for one diagnostic: intents for
// cicada apply, or a command to run. It holds only at Revision, the score's
// revision when it was computed: apply one fix, then check again.
type checkFix struct {
	Summary  string            `json:"summary"`
	Intents  []json.RawMessage `json:"intents,omitempty"`
	Command  []string          `json:"command,omitempty"`
	Revision string            `json:"revision"`
}

type checkBrowserReport struct {
	Tracks            []checkBrowserTrack `json:"tracks"`
	Voices            int                 `json:"voices"`
	MaxVoices         int                 `json:"max_voices"`
	MaxTracks         int                 `json:"max_tracks"`
	ImageBytes        int                 `json:"image_bytes"`
	ImageBytesAtLeast bool                `json:"image_bytes_at_least"`
	MaxImageBytes     int                 `json:"max_image_bytes"`
}

type checkBrowserTrack struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Voices int    `json:"voices"`
}

// checkPathsJSON runs the checks checkPathsWith runs and writes one JSON
// document to stdout instead of text. It returns the same error.
func checkPathsJSON(paths []string, options checkOptions, stdout io.Writer) error {
	document := checkDocument{Schema: checkSchema, Profile: options.profile, Scores: []checkScoreReport{}}
	failed := 0
	for _, path := range paths {
		report := checkScoreJSON(path, options)
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.Severity == "error" {
				failed++
				document.Errors++
			} else {
				document.Warnings++
			}
		}
		document.Scores = append(document.Scores, report)
	}
	document.Valid = failed == 0
	data, err := marshalAgentJSON(document)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, string(data)); err != nil {
		return err
	}
	if failed != 0 {
		return fmt.Errorf("%d error(s) across %d score(s)", failed, len(paths))
	}
	return nil
}

// checkScoreJSON checks one score in the order checkPathsWith does: the
// validator, then the sampler packs, then the browser profile.
func checkScoreJSON(path string, options checkOptions) checkScoreReport {
	report := checkScoreReport{File: jsonPath(path), Diagnostics: []checkDiagnosticReport{}}
	add := func(source []byte, diagnostic notation.Diagnostic, inspection *scoreInspection) {
		report.Diagnostics = append(report.Diagnostics, checkDiagnosticJSON(path, source, diagnostic, inspection))
	}
	inspection, err := inspectScore(path)
	// The revision is of the bytes the checker read, so a fix's revision and
	// the score's agree; a score the checker could not inspect is read again.
	if inspection.source != nil {
		report.Revision = edits.Revision(inspection.source)
	} else if source, readErr := os.ReadFile(path); readErr == nil {
		report.Revision = edits.Revision(source)
	}
	if err != nil {
		var sourceErr *project.SourceError
		if errors.As(err, &sourceErr) {
			add(nil, sourceErr.Diagnostic, nil)
		} else {
			add(nil, notation.Diagnostic{Code: "CICADA-IO", Severity: "error", Message: err.Error()}, nil)
		}
		return report
	}
	for _, diagnostic := range inspection.diagnostics {
		add(inspection.source, diagnostic, &inspection)
	}
	if !hasDiagnosticErrors(inspection.diagnostics) {
		packDiagnostics, packs := checkSamplerPacks(path, inspection, options.maxResidentPCM)
		report.Packs = packs
		for _, diagnostic := range packDiagnostics {
			add(inspection.source, diagnostic, &inspection)
		}
		if !hasDiagnosticErrors(packDiagnostics) && options.profile == profileBrowser {
			features, kinds := portableFeatureRefusals(inspection.semantic)
			for _, diagnostic := range features {
				add(inspection.source, diagnostic, &inspection)
			}
			profile, refusal := checkBrowser(inspection.semantic, inspection.root(path))
			if refusal != nil && !kinds[imageFeatureKind(refusal.Message)] {
				add(inspection.source, *refusal, &inspection)
			}
			browser := &checkBrowserReport{Tracks: []checkBrowserTrack{}, Voices: profile.Voices, MaxVoices: limits.PortableMaxVoices, MaxTracks: limits.PortableMaxTracks, ImageBytes: profile.ImageBytes, ImageBytesAtLeast: profile.AtLeast, MaxImageBytes: kernelimage.MaxImageBytes}
			for _, track := range profile.Tracks {
				browser.Tracks = append(browser.Tracks, checkBrowserTrack{ID: track.ID, Kind: track.Kind, Voices: track.Voices})
			}
			report.Browser = browser
		}
	}
	report.Valid = true
	for _, diagnostic := range report.Diagnostics {
		report.Valid = report.Valid && diagnostic.Severity != "error"
	}
	return report
}

func checkDiagnosticJSON(path string, source []byte, d notation.Diagnostic, inspection *scoreInspection) checkDiagnosticReport {
	file := path
	if d.Position.File != "" && d.Position.File != path {
		file = d.Position.File
		if data, err := os.ReadFile(file); err == nil {
			source = data
		}
	}
	report := checkDiagnosticReport{Code: d.Code, Severity: d.Severity, Message: d.Message, File: jsonPath(file), Hint: checkSuggestion(d)}
	if d.Position.Line > 0 {
		start := sourcePosition(source, d.Position.Line, d.Position.Column)
		end := start
		// The parser reports a point. End marks the end of the character at
		// the start, as the language server does.
		if start.Offset < len(source) && source[start.Offset] != '\n' {
			_, size := utf8.DecodeRune(source[start.Offset:])
			end.Offset += size
			end.Column++
		}
		report.Start, report.End = &start, &end
	}
	if d.Related.Line > 0 {
		relatedSource := source
		relatedFile := file
		if d.Related.File != "" && d.Related.File != file {
			relatedFile = d.Related.File
			relatedSource, _ = os.ReadFile(relatedFile)
		}
		related := sourcePosition(relatedSource, d.Related.Line, d.Related.Column)
		related.File = jsonPath(relatedFile)
		report.Related = &related
	}
	if inspection != nil {
		report.Fix = checkFixFor(path, *inspection, d)
	}
	return report
}

// sourcePosition adds the byte offset to a one-based line and column, where
// the column counts Unicode characters. A position past the end of its line
// or file stops there.
func sourcePosition(source []byte, line, column int) checkPosition {
	at := checkPosition{Line: line, Column: column}
	offset, current := 0, 1
	for offset < len(source) && current < line {
		if source[offset] == '\n' {
			current++
		}
		offset++
	}
	for col := 1; offset < len(source) && col < column && source[offset] != '\n'; col++ {
		_, size := utf8.DecodeRune(source[offset:])
		offset += size
	}
	at.Offset = offset
	return at
}

// checkFixFor returns the repair the checker knows for a diagnostic, or nil.
// A fix is offered only when it is proven on the score as it is: applying it
// alone removes this diagnostic and adds no other (the score may still have
// other errors). Revision is the score's revision the fix was computed for.
func checkFixFor(path string, inspection scoreInspection, d notation.Diagnostic) *checkFix {
	if inspection.source == nil || d.Position.File != "" && d.Position.File != path {
		return nil
	}
	switch {
	case d.Code == "CICADA-ARRANGEMENT" && strings.Contains(d.Message, "must start on a step"):
		return snapPlacementFix(path, inspection, d)
	case d.Code == "CICADA-VERSION":
		fixed, manifestPath, manifest, ok := cicadaFixCandidate(path, inspection.source)
		if ok && fixRemovesWith(path, inspection, d, fixed, manifestPath, manifest) {
			return &checkFix{Summary: "migrate the score with cicada fix", Command: []string{"cicada", "fix", jsonPath(path)}, Revision: edits.Revision(inspection.source)}
		}
	}
	return nil
}

// cicadaFixCandidate is what cicada fix <path> would write for a score that
// is not part of a multi-file project: the fixed source and, when the fix
// writes one, the manifest and its path. It uses the fix command's own
// functions. ok is false when the fix would refuse, change nothing, or touch
// other scores.
func cicadaFixCandidate(path string, source []byte) (fixed []byte, manifestPath string, manifest []byte, ok bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", nil, false
	}
	root, currentEdition, manifestFile, err := fixProjectLocation(absolute)
	if err != nil {
		return nil, "", nil, false
	}
	var original []byte
	if manifestFile != "" {
		if original, err = os.ReadFile(manifestFile); err != nil {
			return nil, "", nil, false
		}
		if parsed, err := edition.ParseProjectManifest(original); err != nil || parsed.ExplicitSources() {
			return nil, "", nil, false
		}
	}
	fixed, changed, err := fixLoadedScoreForProject(absolute, source, currentEdition)
	if err != nil {
		return nil, "", nil, false
	}
	if !changed {
		fixed = source
	}
	newManifest := manifestFile == "" && hasEditionOneEdit([]fixScoreEdit{{path: absolute, before: source, fixed: fixed, changed: changed}}, currentEdition)
	upgrade := manifestFile != "" && currentEdition == 1
	if !changed && !newManifest && !upgrade {
		return nil, "", nil, false
	}
	paths, err := projectScorePaths(root)
	if err != nil {
		return nil, "", nil, false
	}
	for _, other := range paths {
		if filepath.Clean(other) == absolute {
			continue
		}
		if legacy, err := isEditionOneScore(other, currentEdition, manifestFile != "" && !newManifest); err != nil || legacy {
			return nil, "", nil, false
		}
	}
	manifestPath = manifestFile
	switch {
	case newManifest:
		name := strings.TrimSuffix(filepath.Base(absolute), ".cicada")
		if !edition.ValidProjectName(name) {
			return nil, "", nil, false
		}
		manifestPath, manifest = filepath.Join(root, "cicada.mod"), []byte("project "+name+"\ncicada 2\n")
	case upgrade:
		if manifest, _, err = edition.UpgradeManifestEdition(original); err != nil {
			return nil, "", nil, false
		}
	}
	return fixed, manifestPath, manifest, true
}

// fixRemoves reports whether replacing the score's text with candidate
// removes diagnostic d and adds no diagnostic the score did not have.
// Diagnostics are compared by code, message and file, since an edit may move
// lines.
func fixRemoves(path string, inspection scoreInspection, d notation.Diagnostic, candidate []byte) bool {
	return fixRemovesWith(path, inspection, d, candidate, "", nil)
}

// fixRemovesWith is fixRemoves with the manifest the fix writes, when it
// writes one.
func fixRemovesWith(path string, inspection scoreInspection, d notation.Diagnostic, candidate []byte, manifestPath string, manifest []byte) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	overrides := map[string][]byte{absolute: candidate}
	var after scoreInspection
	if manifest != nil {
		overrides[manifestPath] = manifest
		after, err = inspectScorePinned(path, overrides, manifestPath)
	} else {
		after, err = inspectScoreWith(path, overrides)
	}
	if err != nil {
		return false
	}
	key := func(d notation.Diagnostic) string { return d.Code + "\x00" + d.Message + "\x00" + d.Position.File }
	counts := map[string]int{}
	for _, before := range inspection.diagnostics {
		counts[key(before)]++
	}
	for _, now := range after.diagnostics {
		counts[key(now)]--
	}
	if counts[key(d)] < 1 {
		return false
	}
	for _, left := range counts {
		if left < 0 {
			return false
		}
	}
	return true
}

// snapPlacementFix moves a placement that starts between steps to the
// nearest step, as the language server's quick fix does.
func snapPlacementFix(path string, inspection scoreInspection, d notation.Diagnostic) *checkFix {
	score := inspection.score
	if score == nil || score.Arrange == nil {
		return nil
	}
	for _, placement := range score.Arrange.Placements {
		if placement.Position.Line != d.Position.Line || placement.AtTick%seq.TicksPerStep == 0 {
			continue
		}
		snapped := (placement.AtTick + seq.TicksPerStep/2) / seq.TicksPerStep * seq.TicksPerStep
		intent := &edits.MoveClip{Entity: edits.EntityID("placement:" + placement.Name), AtTick: snapped}
		s := &studio{path: path}
		opts, err := s.editOptions(inspection.source, nil)
		if err != nil {
			return nil
		}
		opts.KeepInvalid = true
		env := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}
		result, err := edits.Apply(inspection.source, env, opts)
		if err != nil || result.Unchanged || len(result.Files) != 0 || !fixRemoves(path, inspection, d, result.Source) {
			return nil
		}
		raw, err := env.RawIntents()
		if err != nil {
			return nil
		}
		return &checkFix{Summary: fmt.Sprintf("move placement %s to the nearest step, tick %d", placement.Name, snapped), Intents: raw, Revision: edits.Revision(inspection.source)}
	}
	return nil
}
