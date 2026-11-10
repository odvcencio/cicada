package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
// Peaks are linear full-scale values, as the text summary prints them.
type renderLevelsJSON struct {
	PreLimiterPeak  float64 `json:"pre_limiter_peak"`
	PreLimiterOvers int64   `json:"pre_limiter_overs"`
	OutputPeak      float64 `json:"output_peak"`
	CeilingSamples  int64   `json:"ceiling_samples"`
	ClippedSamples  int64   `json:"clipped_samples"`
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

// renderErrorJSON says why a render failed. Reason is a stable code; the
// message is the text the command prints; remedy is the documented fix, when
// the cause has one.
type renderErrorJSON struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Remedy  string `json:"remedy,omitempty"`
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

// renderJSONCommand runs cicada render or cicada stems with --json: the
// checks and the render are those of the text command, standard output gets
// one JSON document, and diagnostics and the error go to standard error. It
// returns the exit status.
func renderJSONCommand(command, path, output string, opts render.Options, target *renderTargetOptions, stdout, stderr io.Writer) int {
	document := renderDocument{Schema: renderSchema, Command: command, Score: path, Outputs: []renderOutputJSON{}}
	finish := func(err error) int {
		if err != nil {
			document.Error = renderErrorFor(err)
			fmt.Fprintln(stderr, err)
		}
		document.OK = err == nil
		data, marshalErr := marshalAgentJSON(document)
		if marshalErr != nil {
			fmt.Fprintln(stderr, marshalErr)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
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
	document.Levels = &renderLevelsJSON{PreLimiterPeak: float64(report.Peak), PreLimiterOvers: report.PreLimiterOvers, OutputPeak: float64(report.OutputPeak), CeilingSamples: report.CeilingSamples, ClippedSamples: report.ClippedSamples}
	described, err := describeWAV(output, "mix")
	if err != nil {
		return finish(err)
	}
	document.Outputs = append(document.Outputs, described)
	return finish(nil)
}

// renderErrorFor gives an error its stable reason code.
func renderErrorFor(err error) *renderErrorJSON {
	var shortfall *loudnessShortfallError
	if errors.As(err, &shortfall) {
		return &renderErrorJSON{Reason: shortfall.reason, Message: shortfall.message, Remedy: shortfall.remedy}
	}
	var failure *renderFailure
	if errors.As(err, &failure) {
		return &renderErrorJSON{Reason: failure.reason, Message: failure.message}
	}
	return &renderErrorJSON{Reason: "render_failed", Message: err.Error()}
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
	described := renderOutputJSON{Path: path, Role: role}
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
