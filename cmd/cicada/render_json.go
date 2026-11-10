package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// renderSchema names the format of cicada render --json and stems --json.
const renderSchema = "cicada.render.v1"

type renderDocument struct {
	Schema      string                  `json:"schema"`
	Command     string                  `json:"command"`
	Score       string                  `json:"score"`
	OK          bool                    `json:"ok"`
	FromBar     int                     `json:"from_bar,omitempty"`
	Bars        int                     `json:"bars,omitempty"`
	Outputs     []renderOutputJSON      `json:"outputs"`
	Levels      *renderLevelsJSON       `json:"levels,omitempty"`
	Loudness    *renderLoudnessJSON     `json:"loudness,omitempty"`
	Diagnostics []checkDiagnosticReport `json:"diagnostics,omitempty"`
	Error       *renderErrorJSON        `json:"error,omitempty"`
}

// renderOutputJSON is one written WAV file, as its header describes it.
type renderOutputJSON struct {
	Path            string  `json:"path"`
	Role            string  `json:"role"`
	SampleRate      int     `json:"sample_rate"`
	BitDepth        int     `json:"bit_depth"`
	Encoding        string  `json:"encoding"`
	Channels        int     `json:"channels"`
	Frames          int64   `json:"frames"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// renderLevelsJSON are the peak facts of a render without a loudness target.
// Peaks are linear full-scale values, as the text summary prints them. The
// counts use the names of verify-wav --report.
type renderLevelsJSON struct {
	PreLimiterPeak  float64 `json:"pre_limiter_peak"`
	PreLimiterOvers int64   `json:"pre_limiter_overs"`
	OutputPeak      float64 `json:"output_peak"`
	CeilingCount    int64   `json:"ceiling_count"`
	ClippedCount    int64   `json:"clipped_count"`
}

// renderLoudnessJSON is the result of a loudness target. A measurement is
// null when there is none: no pass finished, or the render is too short or
// too quiet to measure.
type renderLoudnessJSON struct {
	TargetLUFS                float64  `json:"target_lufs"`
	ToleranceLU               float64  `json:"tolerance_lu"`
	TruePeakMaxDBTP           float64  `json:"true_peak_max_dbtp"`
	IntegratedLUFS            *float64 `json:"integrated_lufs"`
	TruePeakDBTP              *float64 `json:"true_peak_dbtp"`
	SamplePeakDBFS            *float64 `json:"sample_peak_dbfs"`
	AppliedGainDB             *float64 `json:"applied_gain_db"`
	TargetMet                 bool     `json:"target_met"`
	Passes                    int      `json:"passes"`
	GainStage                 string   `json:"gain_stage,omitempty"`
	MasterLimiterReductionDB  float64  `json:"master_limiter_reduction_db"`
	LargestLimiterReductionDB float64  `json:"largest_limiter_reduction_db"`
	DCCorrectionLeftFS        float64  `json:"dc_correction_left_fs"`
	DCCorrectionRightFS       float64  `json:"dc_correction_right_fs"`
	Notes                     []string `json:"notes,omitempty"`
}

// renderErrorJSON says why a render failed. Code is one of renderErrorCodes;
// the message is the text the command prints; remedy is the documented fix,
// when the cause has one.
type renderErrorJSON struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Remedy  *renderRemedyJSON `json:"remedy,omitempty"`
}

// renderRemedyJSON is a fix for a failed render. ScoreText is the exact text
// to put in the score: when Replaces is set, it replaces that text, which
// occurs exactly once in File at Revision (the declaration it changes);
// otherwise it is added at the end of File. Revision is File's revision when
// the render read it, in the form cicada apply uses: a remedy holds only
// while the file is at that revision. Intents, when present, make the same
// change through cicada apply on the score the render read, in an envelope
// with Revision; it is absent when no intent can.
type renderRemedyJSON struct {
	Summary   string            `json:"summary"`
	File      string            `json:"file"`
	Revision  string            `json:"revision"`
	Replaces  string            `json:"replaces,omitempty"`
	ScoreText string            `json:"score_text"`
	Intents   []json.RawMessage `json:"intents,omitempty"`
}

// renderErrorCodes are the codes of render --json and stems --json errors;
// docs/manual/agents.md lists them and TestRenderErrorCodesAreDocumented
// keeps the lists equal.
var renderErrorCodes = []string{
	"usage", "unreadable_score", "invalid_score", "export_settings", "output_exists",
	"sampler_memory", "render_failed", "true_peak_and_dc_unmet", "loudness_unmeasurable",
	"true_peak_over_ceiling", "limiter_reduction_over_maximum", "loudness_shortfall_at_limiter_maximum",
	"loudness_shortfall_at_input_gain_cap", "loudness_shortfall_at_true_peak_ceiling", "loudness_target_missed",
}

// takeJSONFlag removes --json from a command's flags and reports whether it
// was there.
func takeJSONFlag(args []string) ([]string, bool) {
	kept := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			found = true
			continue
		}
		kept = append(kept, arg)
	}
	return kept, found
}

// renderJSONMain runs cicada render or cicada stems when --json is among
// the arguments: every outcome, a usage error included, prints one document.
// It returns the exit status: 2 for a usage error, as the text command.
func renderJSONMain(command string, args []string, stdout, stderr io.Writer) int {
	usageFailure := func(err error) int {
		fmt.Fprintln(stderr, err)
		writeAgentJSON(stdout, renderDocument{Schema: renderSchema, Command: command, Outputs: []renderOutputJSON{}, Error: &renderErrorJSON{Code: "usage", Message: err.Error()}})
		return 2
	}
	entry, ok := findCommandHelp(command)
	usage := fmt.Errorf("usage: %s", entry.usage)
	if !ok || len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageFailure(usage)
	}
	flags, _ := takeJSONFlag(args[1:])
	wantBits := 24
	if command == "stems" {
		wantBits = 32
	}
	output, opts, target, err := parseRenderArgs(flags, wantBits, stderr)
	if err != nil {
		return usageFailure(err)
	}
	if command == "stems" && target != nil {
		return usageFailure(usage)
	}
	return renderJSONCommand(command, args[0], output, opts, target, stdout, stderr)
}

// renderJSONCommand runs cicada render or cicada stems with --json: the
// checks and the render are those of the text command, standard output gets
// one JSON document, and diagnostics and the error go to standard error. It
// returns the exit status.
func renderJSONCommand(command, path, output string, opts render.Options, target *renderTargetOptions, stdout, stderr io.Writer) int {
	document := renderDocument{Schema: renderSchema, Command: command, Score: jsonPath(path), Outputs: []renderOutputJSON{}}
	var inspection scoreInspection
	finish := func(err error) int {
		if err != nil {
			document.Error = renderErrorFor(err, path, inspection)
			fmt.Fprintln(stderr, err)
		}
		document.OK = err == nil
		if writeErr := writeAgentJSON(stdout, document); writeErr != nil {
			fmt.Fprintln(stderr, writeErr)
			return 1
		}
		if err != nil {
			return 1
		}
		return 0
	}
	inspection, err := inspectScore(path)
	if err != nil {
		return finish(&renderFailure{reason: "unreadable_score", message: err.Error()})
	}
	opts.AssetDir = inspection.root(path)
	if command == "render" && target != nil && target.ExportName != "" {
		if err := applyExportTarget(inspection.semantic, &opts, target); err != nil {
			return finish(&renderFailure{reason: "export_settings", message: err.Error()})
		}
	}
	for _, d := range inspection.diagnostics {
		document.Diagnostics = append(document.Diagnostics, checkDiagnosticJSON(path, inspection.source, d, nil))
		if d.Position.File == "" {
			d.Position.File = path
		}
		fmt.Fprintln(stderr, d.Error())
	}
	if hasDiagnosticErrors(inspection.diagnostics) {
		return finish(&renderFailure{reason: "invalid_score", message: path + " has errors; run cicada check --json " + path})
	}
	opts.AssetRoot = inspection.root(path)
	if command == "stems" {
		if _, err := os.Stat(output); err == nil {
			return finish(&renderFailure{reason: "output_exists", message: "stem output directory already exists: " + output})
		}
		report, err := render.Stems(inspection.score, opts, output)
		if err != nil {
			return finish(err)
		}
		document.FromBar, document.Bars = report.From+1, report.Bars
		entries, err := os.ReadDir(output)
		if err != nil {
			return finish(err)
		}
		var names []string
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".wav" {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			described, err := describeWAV(filepath.Join(output, name), "stem:"+strings.TrimSuffix(name, ".wav"))
			if err != nil {
				return finish(err)
			}
			document.Outputs = append(document.Outputs, described)
		}
		return finish(nil)
	}
	if target != nil && target.Loudness {
		report, err := renderLoudnessFileReport(inspection.score, output, opts, *target, nil)
		loudness := &renderLoudnessJSON{TargetLUFS: target.LoudnessTarget, ToleranceLU: target.Tolerance, TruePeakMaxDBTP: target.TruePeakMaxDBTP, Passes: report.Passes, Notes: report.Notes}
		var shortfall *loudnessShortfallError
		written := err == nil || errors.As(err, &shortfall)
		if written {
			document.FromBar, document.Bars = report.From+1, report.Bars
			loudness.IntegratedLUFS = finiteValue(report.AchievedLUFS)
			loudness.TruePeakDBTP = finiteValue(report.TruePeakDBTP)
			loudness.SamplePeakDBFS = finiteValue(report.SamplePeakDBFS)
			loudness.AppliedGainDB = finiteValue(report.AppliedGainDB)
			loudness.GainStage = report.GainStage
			loudness.MasterLimiterReductionDB = report.MasterLimiterReductionDB
			loudness.LargestLimiterReductionDB = report.LargestLimiterReductionDB
			loudness.DCCorrectionLeftFS, loudness.DCCorrectionRightFS = report.DCCorrection.LeftFS, report.DCCorrection.RightFS
			loudness.TargetMet = err == nil
			if described, describeErr := describeWAV(output, "mix"); describeErr == nil {
				document.Outputs = append(document.Outputs, described)
			} else if err == nil {
				err = describeErr
			}
		}
		document.Loudness = loudness
		return finish(err)
	}
	report, err := renderPlainFile(inspection.score, output, opts)
	if err != nil {
		return finish(err)
	}
	document.FromBar, document.Bars = report.From+1, report.Bars
	document.Levels = &renderLevelsJSON{PreLimiterPeak: float64(report.Peak), PreLimiterOvers: report.PreLimiterOvers, OutputPeak: float64(report.OutputPeak), CeilingCount: report.CeilingSamples, ClippedCount: report.ClippedSamples}
	described, err := describeWAV(output, "mix")
	if err != nil {
		return finish(err)
	}
	document.Outputs = append(document.Outputs, described)
	return finish(nil)
}

// renderErrorFor gives an error its stable code and, for a shortfall that a
// master limiter fixes, the structured remedy for this score.
func renderErrorFor(err error, path string, inspection scoreInspection) *renderErrorJSON {
	var shortfall *loudnessShortfallError
	if errors.As(err, &shortfall) {
		failure := &renderErrorJSON{Code: shortfall.reason, Message: shortfall.message}
		if shortfall.limiterCeiling != nil {
			failure.Remedy = limiterRemedy(path, inspection, *shortfall.limiterCeiling)
		}
		return failure
	}
	var failure *renderFailure
	if errors.As(err, &failure) {
		return &renderErrorJSON{Code: failure.reason, Message: failure.message}
	}
	var memory *instrumentpack.ResidentPCMError
	if errors.As(err, &memory) {
		return &renderErrorJSON{Code: "sampler_memory", Message: err.Error()}
	}
	if strings.Contains(err.Error(), "stem output directory already exists") {
		return &renderErrorJSON{Code: "output_exists", Message: err.Error()}
	}
	return &renderErrorJSON{Code: "render_failed", Message: err.Error()}
}

// limiterRemedy builds the score text that adds a limiter at ceiling dBTP as
// the last master insert: a new fx declaration, and the master declaration
// with that limiter at the end of its insert chain.
func limiterRemedy(path string, inspection scoreInspection, ceiling float64) *renderRemedyJSON {
	remedy := &renderRemedyJSON{Summary: limiterRemedySummary(ceiling), File: jsonPath(path), Revision: edits.Revision(inspection.source)}
	files := []notation.SourceFile{{Path: path, Source: inspection.source}}
	if inspection.sources != nil {
		files = inspection.sources.Files
	}
	names := notation.DeclarationNames(files)
	name := "ceiling"
	for i := 2; names[name]; i++ {
		name = fmt.Sprintf("ceiling-%d", i)
	}
	fx := fmt.Sprintf("fx %s limiter { ceiling = %sdBTP }", name, formatDBTP(ceiling))
	// The same change as intents: addeffect declares the limiter with the
	// ceiling and appends it to the master insert chain.
	envelope := edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{&edits.AddEffect{
		Name: name, EffectKind: "limiter", InsertOn: "master",
		Settings: map[string]json.RawMessage{"ceiling": json.RawMessage(strconv.Quote(formatDBTP(ceiling) + "dBTP"))},
	}}}
	raw, rawErr := envelope.RawIntents()
	remedy.ScoreText = fx + "\nmaster { insert = " + name + " }"
	for _, file := range files {
		if file.Library != "" {
			continue
		}
		master, replaced, ok := masterWithInsert(file.Source, name)
		if !ok {
			continue
		}
		if strings.Count(string(file.Source), master) != 1 {
			// Replacing text that occurs twice would be ambiguous.
			return nil
		}
		remedy.File, remedy.Revision, remedy.Replaces, remedy.ScoreText = jsonPath(file.Path), edits.Revision(file.Source), master, fx+"\n"+replaced
		break
	}
	// cicada apply edits the score it is given, so the intents are offered
	// only when the change belongs to that file.
	if rawErr == nil && remedy.File == jsonPath(path) {
		remedy.Intents = raw
	}
	return remedy
}

// masterWithInsert finds the master declaration of a source and returns its
// text and the same declaration with name appended to its insert chain.
func masterWithInsert(source []byte, name string) (string, string, bool) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return "", "", false
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) != "master_decl" {
			continue
		}
		text := walker.Text(node)
		start := int(node.StartByte())
		var insert *gts.Node
		var find func(*gts.Node)
		find = func(n *gts.Node) {
			for j := 0; j < n.NamedChildCount() && insert == nil; j++ {
				child := n.NamedChild(j)
				if walker.Type(child) == "param_decl" && walker.Text(walker.Field(child, "name")) == "insert" {
					insert = child
					return
				}
				find(child)
			}
		}
		find(node)
		if insert != nil {
			value := walker.Field(insert, "value")
			at := int(value.EndByte()) - start
			current := strings.TrimSpace(walker.Text(value))
			if current == "none" {
				from := int(value.StartByte()) - start
				return text, text[:from] + name + text[at:], true
			}
			return text, text[:at] + " -> " + name + text[at:], true
		}
		closing := strings.LastIndex(text, "}")
		return text, strings.TrimRight(text[:closing], " ") + " insert = " + name + " " + text[closing:], true
	}
	return "", "", false
}

func finiteValue(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

// describeWAV reads the format and length of a written WAV file from its
// header: the fmt chunk and the size of the data chunk.
func describeWAV(path, role string) (renderOutputJSON, error) {
	file, err := os.Open(path)
	if err != nil {
		return renderOutputJSON{}, err
	}
	defer file.Close()
	var riff [12]byte
	if _, err := io.ReadFull(file, riff[:]); err != nil || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WAVE" {
		return renderOutputJSON{}, fmt.Errorf("%s: not a RIFF WAVE file", path)
	}
	described := renderOutputJSON{Path: jsonPath(path), Role: role}
	var blockAlign int
	for {
		var header [8]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return renderOutputJSON{}, fmt.Errorf("%s: no data chunk", path)
		}
		size := int64(binary.LittleEndian.Uint32(header[4:]))
		switch string(header[:4]) {
		case "fmt ":
			chunk := make([]byte, size)
			if _, err := io.ReadFull(file, chunk); err != nil || size < 16 {
				return renderOutputJSON{}, fmt.Errorf("%s: short fmt chunk", path)
			}
			format := binary.LittleEndian.Uint16(chunk[0:])
			if format == 0xfffe && size >= 26 {
				format = binary.LittleEndian.Uint16(chunk[24:])
			}
			described.Encoding = "pcm"
			if format == 3 {
				described.Encoding = "float"
			}
			described.Channels = int(binary.LittleEndian.Uint16(chunk[2:]))
			described.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:]))
			blockAlign = int(binary.LittleEndian.Uint16(chunk[12:]))
			described.BitDepth = int(binary.LittleEndian.Uint16(chunk[14:]))
			if size%2 == 1 {
				file.Seek(1, io.SeekCurrent)
			}
		case "data":
			if blockAlign == 0 || described.SampleRate == 0 {
				return renderOutputJSON{}, fmt.Errorf("%s: data before fmt", path)
			}
			described.Frames = size / int64(blockAlign)
			described.DurationSeconds = math.Round(float64(described.Frames)/float64(described.SampleRate)*1e6) / 1e6
			return described, nil
		default:
			if _, err := file.Seek(size+size%2, io.SeekCurrent); err != nil {
				return renderOutputJSON{}, err
			}
		}
	}
}
