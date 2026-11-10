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
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/migration"
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
}

type checkScoreReport struct {
	File        string                  `json:"file"`
	Valid       bool                    `json:"valid"`
	Diagnostics []checkDiagnosticReport `json:"diagnostics"`
	Browser     *checkBrowserReport     `json:"browser_profile,omitempty"`
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
// cicada apply, or a command to run.
type checkFix struct {
	Summary string            `json:"summary"`
	Intents []json.RawMessage `json:"intents,omitempty"`
	Command []string          `json:"command,omitempty"`
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
	report := checkScoreReport{File: displayPath(path), Diagnostics: []checkDiagnosticReport{}}
	add := func(source []byte, diagnostic notation.Diagnostic, inspection *scoreInspection) {
		report.Diagnostics = append(report.Diagnostics, checkDiagnosticJSON(path, source, diagnostic, inspection))
	}
	inspection, err := inspectScore(path)
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
		packDiagnostics := checkSamplerPacks(path, inspection, options.maxResidentPCM)
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
	report := checkDiagnosticReport{Code: d.Code, Severity: d.Severity, Message: d.Message, File: displayPath(file), Hint: checkSuggestion(d)}
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
		related.File = displayPath(relatedFile)
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
// Each fix is checked: an intent fix applies to the score as it is, and the
// migration fix changes the file.
func checkFixFor(path string, inspection scoreInspection, d notation.Diagnostic) *checkFix {
	switch {
	case d.Code == "CICADA-ARRANGEMENT" && strings.Contains(d.Message, "must start on a step"):
		return snapPlacementFix(path, inspection, d)
	case d.Code == "CICADA-VERSION" && inspection.source != nil:
		if _, changed, err := migration.FixSource(inspection.source); err == nil && changed {
			return &checkFix{Summary: "migrate the legacy spellings with cicada fix", Command: []string{"cicada", "fix", path}}
		}
	}
	return nil
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
		before, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		s := &studio{path: path}
		opts, err := s.editOptions(before, nil)
		if err != nil {
			return nil
		}
		env := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{intent}}
		if result, err := edits.Apply(before, env, opts); err != nil || result.Unchanged {
			return nil
		}
		raw, err := env.RawIntents()
		if err != nil {
			return nil
		}
		return &checkFix{Summary: fmt.Sprintf("move placement %s to the nearest step, tick %d", placement.Name, snapped), Intents: raw}
	}
	return nil
}

// displayPath shows a path under the working directory relative to it, so
// JSON output does not carry absolute temporary paths the user did not type.
func displayPath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}
