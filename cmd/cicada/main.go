// Command cicada provides the first grammar-backed language tools.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/lsp"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func main() {
	if handled, exitCode := handleCLIHelp(os.Args[1:], os.Stdout, os.Stderr); handled {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "studio" {
		if err := studioCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "lsp" {
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: cicada lsp")
			os.Exit(2)
		}
		if err := lsp.Serve(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "new" {
		if err := newCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fix" {
		if err := fixCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		if err := checkCommand(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "play" {
		if err := playCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fields" {
		data, err := project.FieldsJSON()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "params" {
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: cicada params")
			os.Exit(2)
		}
		fmt.Println(project.ParamsJSON())
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "view" {
		if err := viewCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "explain" {
		args := os.Args[2:]
		if (len(args) == 2 || len(args) == 3) && filepath.Ext(args[0]) == ".cicada" && (len(args) == 2 || len(args[2]) > 0 && args[2][0] == '@') {
			location := ""
			if len(args) == 3 {
				location = args[2]
			}
			if err := explainParameter(args[0], args[1], location, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if len(os.Args) < 3 || len(os.Args) > 4 || (len(os.Args) == 4 && os.Args[3] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: cicada explain <construct[.field]> [--json] | cicada explain <score.cicada> <path> [@bar[.beat[.step]]]")
			os.Exit(2)
		}
		if err := explain(os.Args[2], len(os.Args) == 4); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "gen" {
		if err := generatorCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "convert" || os.Args[1] == "compare") {
		projectCommand(os.Args[1:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fmt" {
		formatCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "highlight" {
		highlightCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "symbols" {
		symbolsCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "verify-wav" {
		verifyWAVCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "verify-stems" {
		verifyStemsCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "verify-midi" {
		if err := verifyMIDICommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "compare-midi" {
		if err := compareMIDICommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "import-midi" {
		fmt.Fprintln(os.Stderr, "MIDI import is scheduled for M6; Cicada currently supports midi export and verify-midi")
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "golden" {
		if err := goldenCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 3 || (os.Args[1] != "render" && os.Args[1] != "stems" && os.Args[1] != "midi" && len(os.Args) > 5) {
		usage()
	}
	command := os.Args[1]
	if (command == "validate" || command == "ast") && len(os.Args) != 3 {
		usage()
	}
	if command == "events" && len(os.Args) != 5 {
		usage()
	}
	if command == "graph" && len(os.Args) != 4 {
		usage()
	}
	var renderPath string
	var renderOptions render.Options
	var renderTarget *renderTargetOptions
	var midiOpts midiOptions
	if command == "render" {
		renderPath, renderOptions, renderTarget = renderArgs(os.Args[3:], 24)
	} else if command == "stems" {
		renderPath, renderOptions, renderTarget = renderArgs(os.Args[3:], 32)
		if renderTarget != nil {
			usage()
		}
	} else if command == "midi" {
		midiOpts = parseMIDIArgs(os.Args[3:])
	}
	if command != "validate" && command != "ast" && command != "events" && command != "graph" && command != "render" && command != "stems" && command != "midi" {
		usage()
	}
	path := os.Args[2]
	inspection, err := inspectScore(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	score, semantic, diagnostics := inspection.score, inspection.semantic, inspection.diagnostics
	if command == "render" && renderTarget != nil && renderTarget.ExportName != "" {
		if err := applyExportTarget(semantic, &renderOptions, renderTarget); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	var programs map[string]*instrument.Program
	if command == "graph" && !hasDiagnosticErrors(diagnostics) {
		programs, _ = project.Check(score)
	}
	hasErrors := false
	for _, d := range diagnostics {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: %s %s: %s\n", path, d.Position.Line, d.Position.Column, d.Severity, d.Code, d.Message)
		if d.Severity == "error" {
			hasErrors = true
		}
	}
	if hasErrors {
		os.Exit(1)
	}
	switch command {
	case "validate":
		fmt.Println(path)
	case "ast":
		writeJSON(score)
	case "events":
		if err := emitEvents(score, os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "graph":
		program := programs[os.Args[3]]
		if program == nil {
			fmt.Fprintln(os.Stderr, "unknown instrument", os.Args[3])
			os.Exit(1)
		}
		writeJSON(program)
	case "render":
		if err := renderFile(score, renderPath, renderOptions, renderTarget); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "stems":
		report, err := render.Stems(score, renderOptions, renderPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %d stems, %d bars from bar %d, %d frames at %d Hz\n", renderPath, len(score.Tracks)+5, report.Bars, report.From+1, report.Frames, report.SampleRate)
	case "midi":
		if err := midiFile(semantic, midiOpts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func verifyStemsCommand(args []string) {
	if len(args) < 1 {
		usage()
	}
	flags := flag.NewFlagSet("verify-stems", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tap := flags.String("tap", "pre-comp", "tap to verify")
	residual := flags.Float64("residual-max-db", -80, "maximum bus sum residual in dBFS")
	if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 || *tap != "pre-comp" {
		usage()
	}
	report, err := render.VerifyStems(nil, args[0], render.VerifyStemsOptions{ResidualMaxDB: *residual})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d float32 stems, %d frames, pre-comp residual %.2f dBFS\n", args[0], report.Files, report.Frames, report.ResidualPeakDB)
}

func hasDiagnosticErrors(diagnostics []notation.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func appendUniqueDiagnostics(existing, extra []notation.Diagnostic) []notation.Diagnostic {
	seen := make(map[notation.Diagnostic]bool, len(existing)+len(extra))
	for _, diagnostic := range existing {
		seen[diagnostic] = true
	}
	for _, diagnostic := range extra {
		if !seen[diagnostic] {
			existing = append(existing, diagnostic)
			seen[diagnostic] = true
		}
	}
	return existing
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cicada "+
		"new <name> | check [score.cicada] | validate <score.cicada> | fix <score.cicada> [--all] [--check] | play [score.cicada] [--audio tymbal|oto|null] | lsp | studio [score.cicada] [--listen 127.0.0.1:port] [--audio tymbal|oto|null] [--lsp-stdio] | "+
		"gen --seed N --key a --scale minor [-o out.cicada] [--trace] | "+
		"validate|ast <file.cicada> | events <file.cicada> <track> <pattern> | graph <file.cicada> <instrument> | "+
		"render <file.cicada> -o <out.wav> [--rate 48000 --bits 16|24|32 --from 1 --bars 16 --tail 3s --dither=true --normalize=false --block 4096 --loudness -14 --true-peak-max -1 --loudness-tolerance 0.5] | "+
		"stems <file.cicada> -o <dir> [--rate 48000 --from 1 --bars 16 --tail 3s] | verify-stems <dir> [--tap pre-comp --residual-max-db -80] | "+
		"midi <file.cicada> -o <out.mid> [--bars 16 --pattern name --report] | verify-midi <file.mid> --ppq 960 --type 1 | compare-midi <a.mid> <b.mid> | "+
		"verify-wav <file.wav> --rate 48000 --bits 16|24|32 --from 1 --bars 16 --tail 3s --peak-max-db -0.3 --dc-max-db -60 [--lufs -14 --lufs-tolerance 0.5 --true-peak-max -1 --report] | "+
		"golden [--update] [--score file.cicada] [--out file.fp] [--rate 48000] [--bars 8] | fmt [--check|-w] [file.cicada] | "+
		"convert <in> -o <out> | compare --semantic <a> <b> | view <in.cicada|in.json> -o <out.html> | fields | params | explain <construct[.field]> [--json] | explain <score.cicada> <path> [@bar[.beat[.step]]] | highlight [--html|--spans] <file.cicada> | symbols [--refs] [--json] <file.cicada>")
	os.Exit(2)
}

func formatCommand(args []string) {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--check" || args[0] == "-w") {
		check := len(args) == 1 && args[0] == "--check"
		if err := formatProjectCommand(check, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	mode := "stdout"
	if len(args) == 2 {
		if args[0] == "--check" || args[0] == "-w" {
			mode, args = args[0], args[1:]
		} else {
			mode, args = args[1], args[:1]
		}
	}
	if len(args) != 1 || (mode != "stdout" && mode != "--check" && mode != "-w") {
		usage()
	}
	path := args[0]
	source, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var formatted []byte
	if filepath.Ext(path) == ".json" {
		var p *project.Project
		p, err = project.DecodeJSON(source)
		if err == nil {
			formatted, err = project.CanonicalJSON(p)
		}
	} else if filepath.Ext(path) == ".cicada" {
		var document *notation.Document
		document, err = notation.ParseDocument(source)
		if err == nil {
			formatted, err = notation.Format(document)
		}
	} else {
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch mode {
	case "--check":
		if !bytes.Equal(source, formatted) {
			fmt.Fprintln(os.Stderr, "--- "+path)
			fmt.Fprintln(os.Stderr, "+++ "+path+" (formatted)")
			fmt.Fprint(os.Stderr, simpleDiff(source, formatted))
			os.Exit(1)
		}
	case "-w":
		if !bytes.Equal(source, formatted) {
			if err := writeAtomic(path, formatted); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	default:
		if _, err := os.Stdout.Write(formatted); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func simpleDiff(before, after []byte) string {
	oldLines := bytes.Split(bytes.TrimSuffix(before, []byte("\n")), []byte("\n"))
	newLines := bytes.Split(bytes.TrimSuffix(after, []byte("\n")), []byte("\n"))
	var out bytes.Buffer
	fmt.Fprintf(&out, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		fmt.Fprintf(&out, "-%s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(&out, "+%s\n", line)
	}
	return out.String()
}

func writeAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cicada-format-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

type renderTargetOptions struct {
	LoudnessTarget    float64
	TruePeakMaxDBTP   float64
	Tolerance         float64
	Loudness          bool
	ExportName        string
	ExplicitRate      bool
	ExplicitBits      bool
	ExplicitTail      bool
	ExplicitNormalize bool
	ExplicitLoudness  bool
	ExplicitTruePeak  bool
	ExplicitTolerance bool
}

func applyExportTarget(p *project.Project, opts *render.Options, target *renderTargetOptions) error {
	if p == nil || opts == nil || target == nil {
		return fmt.Errorf("export settings need a compiled project")
	}
	var selected *project.Export
	for i := range p.Exports {
		if p.Exports[i].ID == target.ExportName {
			selected = &p.Exports[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown export %s", target.ExportName)
	}
	if selected.Rate != nil && !target.ExplicitRate {
		opts.SampleRate = *selected.Rate
	}
	if selected.Bits != nil && !target.ExplicitBits {
		opts.Bits = *selected.Bits
	}
	if selected.Tail != nil && selected.Tail.Number != nil && !target.ExplicitTail {
		opts.TailSec = *selected.Tail.Number / 1000
	}
	if selected.Normalize != nil && !target.ExplicitNormalize {
		opts.Normalize = *selected.Normalize
	}
	if selected.Loudness != nil && selected.Loudness.Number != nil && !target.ExplicitLoudness {
		target.LoudnessTarget = *selected.Loudness.Number
		target.Loudness = true
	}
	if selected.TruePeak != nil && selected.TruePeak.Number != nil && !target.ExplicitTruePeak {
		target.TruePeakMaxDBTP = *selected.TruePeak.Number
	}
	if target.Loudness && !target.ExplicitTolerance {
		target.Tolerance = 0.5
	}
	if selected.TruePeak != nil && selected.Loudness == nil && !target.ExplicitLoudness {
		return fmt.Errorf("CICADA-UNSUPPORTED: export %s true_peak without loudness targeting is not implemented", selected.ID)
	}
	if target.Loudness && opts.Normalize {
		return fmt.Errorf("CICADA-UNSUPPORTED: export %s cannot combine loudness targeting and normalize", selected.ID)
	}
	return nil
}

const loudnessPassLimit = 6

func renderFile(score *notation.Score, path string, opts render.Options, target *renderTargetOptions) error {
	if target != nil && target.Loudness {
		return renderLoudnessFile(score, path, opts, *target)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cicada-render-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if info, err := os.Stat(path); err == nil {
		if err := file.Chmod(info.Mode().Perm()); err != nil {
			file.Close()
			return err
		}
	} else if !os.IsNotExist(err) {
		file.Close()
		return err
	}
	report, renderErr := render.WAV(score, opts, file)
	if renderErr == nil {
		renderErr = file.Sync()
	}
	closeErr := file.Close()
	if renderErr != nil {
		return renderErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	fmt.Printf("%s: %d bars from bar %d, %d frames at %d Hz, pre-limiter peak %.3f, pre-limiter overs %d, output peak %.3f, ceiling samples %d, clipped samples %d\n", path, report.Bars, report.From+1, report.Frames, report.SampleRate, report.Peak, report.PreLimiterOvers, report.OutputPeak, report.CeilingSamples, report.ClippedSamples)
	return nil
}

func renderLoudnessFile(score *notation.Score, path string, opts render.Options, target renderTargetOptions) error {
	report, err := renderLoudnessFileReport(score, path, opts, target, nil)
	if report.Passes > 0 {
		fmt.Printf("%s: %d bars from bar %d, %d frames at %d Hz, achieved %.2f LUFS, true peak %.2f dBTP, applied gain %+.2f dB, DC correction L %+.7f/R %+.7f FS, passes %d, largest limiter gain reduction %.2f dB\n",
			path, report.Bars, report.From+1, report.Frames, report.SampleRate, report.AchievedLUFS, report.TruePeakDBTP, report.AppliedGainDB, report.DCCorrection.LeftFS, report.DCCorrection.RightFS, report.Passes, report.LargestLimiterReductionDB)
	}
	return err
}

type loudnessFileReport struct {
	TargetLUFS                float64 `json:"target_lufs"`
	AchievedLUFS              float64 `json:"achieved_lufs"`
	TruePeakDBTP              float64 `json:"true_peak_dbtp"`
	SamplePeakDBFS            float64 `json:"sample_peak_dbfs"`
	AppliedGainDB             float64 `json:"applied_gain_db"`
	Passes                    int     `json:"passes"`
	LargestLimiterReductionDB float64 `json:"largest_limiter_reduction_db"`
	DCCorrection              struct {
		LeftFS  float64 `json:"left_fs"`
		RightFS float64 `json:"right_fs"`
	} `json:"dc_correction"`
	Bars       int   `json:"-"`
	From       int   `json:"-"`
	Frames     int64 `json:"-"`
	SampleRate int   `json:"-"`
}

func renderLoudnessFileReport(score *notation.Score, path string, opts render.Options, target renderTargetOptions, onPass func(int)) (loudnessFileReport, error) {
	return renderLoudnessFileReportWithShortfall(score, path, "", opts, target, onPass)
}

func renderStudioLoudnessFileReport(score *notation.Score, path string, opts render.Options, target renderTargetOptions, onPass func(int)) (loudnessFileReport, error) {
	return renderLoudnessFileReportWithShortfall(score, path, studioShortfallPath(path), opts, target, onPass)
}

type loudnessShortfallError struct{ message string }

func (e *loudnessShortfallError) Error() string { return e.message }

func renderLoudnessFileReportWithShortfall(score *notation.Score, path, shortfallPath string, opts render.Options, target renderTargetOptions, onPass func(int)) (loudnessFileReport, error) {
	report := loudnessFileReport{TargetLUFS: target.LoudnessTarget}
	dir := filepath.Dir(path)
	candidate, err := os.CreateTemp(dir, ".cicada-loudness-candidate-*")
	if err != nil {
		return report, err
	}
	candidatePath := candidate.Name()
	if err := candidate.Close(); err != nil {
		os.Remove(candidatePath)
		return report, err
	}
	defer os.Remove(candidatePath)
	best, err := os.CreateTemp(dir, ".cicada-loudness-best-*")
	if err != nil {
		return report, err
	}
	bestPath := best.Name()
	if err := best.Close(); err != nil {
		os.Remove(bestPath)
		return report, err
	}
	defer os.Remove(bestPath)

	var bestRender render.Report
	var bestMeasure render.VerifyReport
	bestGain, bestDelta, hasBest := 0.0, math.Inf(1), false
	var bestBiasL, bestBiasR float32
	var peakFallbackRender render.Report
	var peakFallbackMeasure render.VerifyReport
	peakFallbackExcess := math.Inf(1)
	peakFallbackGain := 0.0
	var peakFallbackBiasL, peakFallbackBiasR float32
	passes := 0
	gain := 0.0
	var biasL, biasR float32
	for pass := 1; pass <= loudnessPassLimit; pass++ {
		passes = pass
		if onPass != nil {
			onPass(pass)
		}
		passOptions := opts
		passOptions.MasterGainDB = gain
		passOptions.MasterBiasL, passOptions.MasterBiasR = biasL, biasR
		passOptions.Normalize = false
		file, err := os.OpenFile(candidatePath, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return report, err
		}
		passReport, renderErr := render.WAV(score, passOptions, file)
		if renderErr == nil {
			renderErr = file.Sync()
		}
		closeErr := file.Close()
		if renderErr != nil {
			return report, renderErr
		}
		if closeErr != nil {
			return report, closeErr
		}
		measurement, err := render.VerifyWAV(candidatePath, render.VerifyOptions{
			SampleRate: passReport.SampleRate,
			Bits:       opts.Bits,
			Bars:       passReport.Bars,
			From:       passReport.From,
			TailSec:    opts.TailSec,
			PeakMaxDB:  100,
			DCMaxDB:    100,
		})
		if err != nil {
			return report, err
		}
		peakSafe := measurement.TruePeakDBTP <= target.TruePeakMaxDBTP
		dcSafe := measurement.DCDB <= -60
		safe := peakSafe && dcSafe
		if dcSafe && !peakSafe && !hasBest {
			excess := measurement.TruePeakDBTP - target.TruePeakMaxDBTP
			if excess < peakFallbackExcess {
				if err := copyRenderFile(candidatePath, bestPath); err != nil {
					return report, err
				}
				peakFallbackRender, peakFallbackMeasure, peakFallbackGain = passReport, measurement, gain
				peakFallbackBiasL, peakFallbackBiasR, peakFallbackExcess = biasL, biasR, excess
			}
		}
		if safe {
			delta := math.Abs(measurement.IntegratedLUFS - target.LoudnessTarget)
			if !hasBest || delta < bestDelta {
				if err := copyRenderFile(candidatePath, bestPath); err != nil {
					return report, err
				}
				bestRender, bestMeasure, bestGain, bestDelta, bestBiasL, bestBiasR, hasBest = passReport, measurement, gain, delta, biasL, biasR, true
			}
			if delta <= target.Tolerance {
				break
			}
		}
		if !dcSafe {
			gainLinear := math.Pow(10, gain/20)
			biasL -= float32(measurement.DCLeft / gainLinear)
			biasR -= float32(measurement.DCRight / gainLinear)
		}
		if !peakSafe {
			gain -= measurement.TruePeakDBTP - target.TruePeakMaxDBTP + 0.05
		} else if measurement.IntegratedLUFS < target.LoudnessTarget-target.Tolerance {
			headroom := target.TruePeakMaxDBTP - measurement.TruePeakDBTP - 0.05
			if headroom <= 0 {
				break
			}
			gain += math.Min(target.LoudnessTarget-measurement.IntegratedLUFS, headroom)
		} else {
			gain += target.LoudnessTarget - measurement.IntegratedLUFS
		}
		gain = math.Max(-120, math.Min(24, gain))
	}
	if !hasBest {
		if math.IsInf(peakFallbackExcess, 1) {
			return report, fmt.Errorf("could not meet the true-peak ceiling %.2f dBTP and -60 dBFS DC limit in %d passes", target.TruePeakMaxDBTP, passes)
		}
		bestRender, bestMeasure, bestGain = peakFallbackRender, peakFallbackMeasure, peakFallbackGain
		bestBiasL, bestBiasR = peakFallbackBiasL, peakFallbackBiasR
		bestDelta = math.Abs(bestMeasure.IntegratedLUFS - target.LoudnessTarget)
		hasBest = true
	}
	shortfallErr := (*loudnessShortfallError)(nil)
	if math.IsNaN(bestMeasure.IntegratedLUFS) || math.IsInf(bestMeasure.IntegratedLUFS, 0) {
		shortfallErr = &loudnessShortfallError{message: fmt.Sprintf("loudness target %.2f LUFS was not reached: render has no measurable loudness", target.LoudnessTarget)}
	} else if bestMeasure.TruePeakDBTP > target.TruePeakMaxDBTP {
		shortfallErr = &loudnessShortfallError{message: fmt.Sprintf("true peak %.2f dBTP exceeds ceiling %.2f dBTP", bestMeasure.TruePeakDBTP, target.TruePeakMaxDBTP)}
	} else if bestDelta > target.Tolerance {
		shortfall := target.LoudnessTarget - bestMeasure.IntegratedLUFS
		if shortfall > target.Tolerance && bestMeasure.TruePeakDBTP >= target.TruePeakMaxDBTP-0.1 {
			shortfallErr = &loudnessShortfallError{message: fmt.Sprintf("loudness target shortfall %.2f LU: achieved %.2f LUFS at true-peak ceiling %.2f dBTP (target %.2f LUFS)", shortfall, bestMeasure.IntegratedLUFS, bestMeasure.TruePeakDBTP, target.LoudnessTarget)}
		} else {
			shortfallErr = &loudnessShortfallError{message: fmt.Sprintf("loudness target %.2f LUFS missed by %.2f LU (tolerance %.2f LU); achieved %.2f LUFS", target.LoudnessTarget, bestDelta, target.Tolerance, bestMeasure.IntegratedLUFS)}
		}
	}
	destination := path
	if shortfallErr != nil && shortfallPath != "" {
		destination = shortfallPath
	}
	if info, err := os.Stat(destination); err == nil {
		if err := os.Chmod(bestPath, info.Mode().Perm()); err != nil {
			return report, err
		}
	} else if !os.IsNotExist(err) {
		return report, err
	}
	if err := os.Rename(bestPath, destination); err != nil {
		return report, err
	}
	report.AchievedLUFS = bestMeasure.IntegratedLUFS
	report.TruePeakDBTP = bestMeasure.TruePeakDBTP
	report.SamplePeakDBFS = bestMeasure.PeakDB
	report.AppliedGainDB = bestGain
	report.Passes = passes
	report.LargestLimiterReductionDB = bestRender.MaxLimiterGainReductionDB
	report.DCCorrection.LeftFS, report.DCCorrection.RightFS = float64(bestBiasL), float64(bestBiasR)
	report.Bars, report.From, report.Frames, report.SampleRate = bestRender.Bars, bestRender.From, bestRender.Frames, bestRender.SampleRate
	if shortfallErr != nil {
		return report, shortfallErr
	}
	return report, nil
}

func copyRenderFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func renderArgs(args []string, wantBits int) (string, render.Options, *renderTargetOptions) {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "output WAV")
	rate := flags.Int("rate", 48_000, "sample rate")
	bits := flags.Int("bits", wantBits, "WAV bit depth")
	bars := flags.Int("bars", 0, "bars to render; 0 is the remaining song")
	from := flags.Int("from", 1, "one-based start bar; 1 is the first bar")
	tail := flags.String("tail", "3s", "tail duration")
	dither := flags.Bool("dither", true, "deterministic TPDF dither for integer PCM")
	normalize := flags.Bool("normalize", false, "peak normalize output to -1 dBFS")
	block := flags.Int("block", 4096, "offline render block size")
	loudness := flags.Float64("loudness", math.NaN(), "target integrated loudness in LUFS")
	truePeak := flags.Float64("true-peak-max", -1, "maximum true peak in dBTP for loudness rendering")
	loudnessTolerance := flags.Float64("loudness-tolerance", 0.5, "loudness target tolerance in LU")
	exportName := flags.String("export", "", "named export settings block")
	if err := flags.Parse(args); err != nil || *output == "" || len(flags.Args()) != 0 || (*bits != 16 && *bits != 24 && *bits != 32) || (wantBits == 32 && *bits != 32) {
		usage()
	}
	fromBar, err := cliBarOffset(*from, flags, os.Stderr)
	if err != nil {
		usage()
	}
	duration, err := time.ParseDuration(*tail)
	if err != nil || duration < 0 {
		usage()
	}
	var target *renderTargetOptions
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "loudness" {
			if math.IsNaN(*loudness) || math.IsInf(*loudness, 0) || math.IsNaN(*truePeak) || math.IsInf(*truePeak, 0) || math.IsNaN(*loudnessTolerance) || math.IsInf(*loudnessTolerance, 0) || *loudnessTolerance < 0 || *loudnessTolerance > 10 || *normalize {
				usage()
			}
			target = &renderTargetOptions{LoudnessTarget: *loudness, TruePeakMaxDBTP: *truePeak, Tolerance: *loudnessTolerance, Loudness: true}
		}
	})
	if *exportName != "" {
		if target == nil {
			target = &renderTargetOptions{TruePeakMaxDBTP: -1, Tolerance: 0.5}
		}
		target.ExportName = *exportName
	}
	if target != nil {
		flags.Visit(func(value *flag.Flag) {
			switch value.Name {
			case "rate":
				target.ExplicitRate = true
			case "bits":
				target.ExplicitBits = true
			case "tail":
				target.ExplicitTail = true
			case "normalize":
				target.ExplicitNormalize = true
			case "loudness":
				target.ExplicitLoudness = true
			case "true-peak-max":
				target.ExplicitTruePeak = true
			case "loudness-tolerance":
				target.ExplicitTolerance = true
			}
		})
	}
	return *output, render.Options{SampleRate: *rate, Bits: *bits, Bars: *bars, From: fromBar, TailSec: duration.Seconds(), Dither: dither, Normalize: *normalize, Block: *block}, target
}

func verifyWAVCommand(args []string) {
	if len(args) < 1 {
		usage()
	}
	path := args[0]
	flags := flag.NewFlagSet("verify-wav", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rate := flags.Int("rate", 48_000, "sample rate")
	bits := flags.Int("bits", 24, "PCM bit depth")
	bars := flags.Int("bars", 0, "expected bars")
	from := flags.Int("from", 1, "one-based start bar; 1 is the first bar")
	tail := flags.String("tail", "3s", "expected tail")
	peak := flags.Float64("peak-max-db", -.3, "peak ceiling in dBFS")
	dc := flags.Float64("dc-max-db", -60, "DC ceiling in dBFS")
	lufs := flags.Float64("lufs", math.NaN(), "target integrated loudness in LUFS")
	lufsTolerance := flags.Float64("lufs-tolerance", 0.5, "loudness target tolerance in LU")
	truePeak := flags.Float64("true-peak-max", math.NaN(), "maximum true peak in dBTP")
	jsonReport := flags.Bool("report", false, "write the verification report as JSON to stderr")
	if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 || *bars == 0 {
		usage()
	}
	fromBar, err := cliBarOffset(*from, flags, os.Stderr)
	if err != nil {
		usage()
	}
	duration, err := time.ParseDuration(*tail)
	if err != nil || duration < 0 {
		usage()
	}
	checkLUFS := false
	checkTruePeak := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "lufs" {
			checkLUFS = true
		}
		if value.Name == "true-peak-max" {
			checkTruePeak = true
		}
	})
	report, err := render.VerifyWAV(path, render.VerifyOptions{
		SampleRate: *rate, Bits: *bits, Bars: *bars, From: fromBar, TailSec: duration.Seconds(),
		PeakMaxDB: *peak, DCMaxDB: *dc, CheckLUFS: checkLUFS, LUFSTarget: *lufs,
		LUFSTolerance: *lufsTolerance, CheckTruePeak: checkTruePeak, TruePeakMaxDBTP: *truePeak,
	})
	if *jsonReport {
		if reportErr := writeVerifyJSON(report); reportErr != nil {
			fmt.Fprintln(os.Stderr, reportErr)
			os.Exit(1)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d frames, peak %s dBFS, DC %s dBFS; true peak %s dBTP, RMS %s dBFS, integrated %s LUFS, loudness range %.2f LU, maximum momentary %s LUFS, maximum short-term %s LUFS, clipped samples %d, ceiling samples %d\n",
		path, report.Frames, formatDB(report.PeakDB), formatDB(report.DCDB), formatDB(report.TruePeakDBTP), formatDB(report.RMSDBFS),
		formatDB(report.IntegratedLUFS), report.LoudnessRange, formatDB(report.MaxMomentaryLUFS), formatDB(report.MaxShortTermLUFS), report.ClippedSamples, report.CeilingSamples)
}

func formatDB(value float64) string {
	if math.IsInf(value, -1) {
		return "-inf"
	}
	if math.IsInf(value, 1) {
		return "+inf"
	}
	return fmt.Sprintf("%.2f", value)
}

func cliBarOffset(bar int, flags *flag.FlagSet, warning io.Writer) (int, error) {
	if bar == 0 {
		provided := false
		flags.Visit(func(value *flag.Flag) {
			provided = provided || value.Name == "from"
		})
		if provided {
			fmt.Fprintln(warning, "warning: --from 0 is deprecated; use --from 1 for the first bar")
		}
		return 0, nil
	}
	if bar < 1 {
		return 0, fmt.Errorf("bar numbers start at 1")
	}
	return bar - 1, nil
}

func writeVerifyJSON(report render.VerifyReport) error {
	value := func(input float64) any {
		if math.IsInf(input, 0) || math.IsNaN(input) {
			return nil
		}
		return input
	}
	data := map[string]any{
		"sample_rate":         report.SampleRate,
		"bars":                report.Bars,
		"from":                report.From + 1,
		"frames":              report.Frames,
		"sample_peak_dbfs":    value(report.PeakDB),
		"true_peak_dbtp":      value(report.TruePeakDBTP),
		"rms_dbfs":            value(report.RMSDBFS),
		"integrated_lufs":     value(report.IntegratedLUFS),
		"loudness_range_lu":   value(report.LoudnessRange),
		"max_momentary_lufs":  value(report.MaxMomentaryLUFS),
		"max_short_term_lufs": value(report.MaxShortTermLUFS),
		"dc_dbfs":             value(report.DCDB),
		"clipped_count":       report.ClippedSamples,
		"ceiling_count":       report.CeilingSamples,
	}
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type eventRecord struct {
	Lane string `json:"lane,omitempty"`
	seq.Event
}

func emitEvents(score *notation.Score, trackName, patternName string) error {
	var track *notation.Track
	var pattern *notation.Pattern
	for i := range score.Tracks {
		if score.Tracks[i].Name == trackName {
			track = &score.Tracks[i]
		}
	}
	for i := range score.Patterns {
		if score.Patterns[i].Name == patternName {
			pattern = &score.Patterns[i]
		}
	}
	if track == nil || pattern == nil {
		return fmt.Errorf("unknown track or pattern")
	}
	compiled, err := project.CompilePattern(score, *pattern, *track)
	if err != nil {
		return err
	}
	clock, err := seq.NewClock(48000, score.TempoMilli)
	if err != nil {
		return err
	}
	end := clock.SampleAtTick(seq.TicksPerBar)
	var records []eventRecord
	var buf [128]seq.Event
	for _, cp := range compiled {
		for sample := int64(0); sample < end; sample += 4096 {
			frames := 4096
			if sample+int64(frames) > end {
				frames = int(end - sample)
			}
			n, overflow := seq.EventsInBlock(&cp.Pattern, clock, 0, 0, sample, frames, buf[:])
			if overflow {
				return fmt.Errorf("event buffer overflow")
			}
			for _, event := range buf[:n] {
				records = append(records, eventRecord{Lane: cp.Lane, Event: event})
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Sample != records[j].Sample {
			return records[i].Sample < records[j].Sample
		}
		return records[i].Lane < records[j].Lane
	})
	writeJSON(records)
	return nil
}
