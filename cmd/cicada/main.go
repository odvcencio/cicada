// Command cicada provides the first grammar-backed language tools.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func main() {
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
	if len(os.Args) > 1 && os.Args[1] == "verify-wav" {
		verifyWAVCommand(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "golden" {
		if err := goldenCommand(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 3 || (os.Args[1] != "render" && os.Args[1] != "stems" && os.Args[1] != "verify-stems" && len(os.Args) > 5) {
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
	if command == "render" {
		renderPath, renderOptions = renderArgs(os.Args[3:], 24)
	} else if command == "stems" {
		renderPath, renderOptions = renderArgs(os.Args[3:], 32)
	}
	if command != "validate" && command != "ast" && command != "events" && command != "graph" && command != "render" && command != "stems" && command != "verify-stems" {
		usage()
	}
	path := os.Args[2]
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	score, diagnostics := notation.Parse(src)
	var programs map[string]*instrument.Program
	parseHasError := false
	for _, d := range diagnostics {
		if d.Severity == "error" {
			parseHasError = true
		}
	}
	if !parseHasError {
		// Conversion and the live engine use the typed project. Validate through
		// that same gate so a source file cannot pass here and fail to load.
		semantic, projectDiagnostics := project.FromScore(score)
		diagnostics = appendUniqueDiagnostics(diagnostics, projectDiagnostics)
		if semantic != nil {
			if _, err := project.CompileEngine(semantic, 48_000, 128); err != nil {
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: "CICADA-PARAM", Severity: "error", Message: err.Error(),
					Position: notation.Position{Line: 1, Column: 1},
				})
			}
		}
		if command == "graph" && !hasDiagnosticErrors(diagnostics) {
			programs, _ = project.Check(score)
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
		if err := renderFile(score, renderPath, renderOptions); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "stems":
		report, err := render.Stems(score, renderOptions, renderPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %d stems, %d bars, %d frames at %d Hz\n", renderPath, len(score.Tracks)+5, report.Bars, report.Frames, report.SampleRate)
	case "verify-stems":
		if len(os.Args) < 4 {
			usage()
		}
		flags := flag.NewFlagSet("verify-stems", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		tap := flags.String("tap", "pre-comp", "tap to verify")
		residual := flags.Float64("residual-max-db", -80, "maximum bus sum residual in dBFS")
		if err := flags.Parse(os.Args[4:]); err != nil || len(flags.Args()) != 0 || *tap != "pre-comp" {
			usage()
		}
		report, err := render.VerifyStems(score, os.Args[3], render.VerifyStemsOptions{ResidualMaxDB: *residual})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s: %d float32 stems, %d frames, pre-comp residual %.2f dBFS\n", os.Args[3], report.Files, report.Frames, report.ResidualPeakDB)
	}
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
	fmt.Fprintln(os.Stderr, "usage: cicada gen --seed N --key a --scale minor [-o out.cicada] [--trace] | validate|ast <file.cicada> | events <file.cicada> <track> <pattern> | graph <file.cicada> <instrument> | render <file.cicada> -o <out.wav> [--rate 48000 --bits 24 --bars 16 --tail 3s] | stems <file.cicada> -o <dir> [--rate 48000 --bars 16 --tail 3s] | verify-stems <file.cicada> <dir> [--tap pre-comp --residual-max-db -80] | verify-wav <file.wav> --rate 48000 --bits 24 --bars 16 --tail 3s --peak-max-db -0.3 --dc-max-db -60 | golden [--update] [--score file.cicada] [--out file.fp] [--rate 48000] [--bars 8] | fmt [--check|-w] <file.cicada> | convert <in> -o <out> | compare --semantic <a> <b>")
	os.Exit(2)
}

func formatCommand(args []string) {
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

func renderFile(score *notation.Score, path string, opts render.Options) error {
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
	fmt.Printf("%s: %d bars, %d frames at %d Hz, pre-limiter peak %.3f, pre-limiter overs %d, output peak %.3f, ceiling samples %d, clipped samples %d\n", path, report.Bars, report.Frames, report.SampleRate, report.Peak, report.PreLimiterOvers, report.OutputPeak, report.CeilingSamples, report.ClippedSamples)
	return nil
}

func renderArgs(args []string, wantBits int) (string, render.Options) {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "output WAV")
	rate := flags.Int("rate", 48_000, "sample rate")
	bits := flags.Int("bits", wantBits, "WAV bit depth")
	bars := flags.Int("bars", 0, "bars to render; 0 is the full song")
	tail := flags.String("tail", "3s", "tail duration")
	if err := flags.Parse(args); err != nil || *output == "" || len(flags.Args()) != 0 || *bits != wantBits {
		usage()
	}
	duration, err := time.ParseDuration(*tail)
	if err != nil || duration < 0 {
		usage()
	}
	return *output, render.Options{SampleRate: *rate, Bars: *bars, TailSec: duration.Seconds()}
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
	tail := flags.String("tail", "3s", "expected tail")
	peak := flags.Float64("peak-max-db", -.3, "peak ceiling in dBFS")
	dc := flags.Float64("dc-max-db", -60, "DC ceiling in dBFS")
	if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 || *bars == 0 {
		usage()
	}
	duration, err := time.ParseDuration(*tail)
	if err != nil || duration < 0 {
		usage()
	}
	report, err := render.VerifyWAV(path, render.VerifyOptions{SampleRate: *rate, Bits: *bits, Bars: *bars, TailSec: duration.Seconds(), PeakMaxDB: *peak, DCMaxDB: *dc})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d frames, peak %.2f dBFS, DC %.2f dBFS\n", path, report.Frames, report.PeakDB, report.DCDB)
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
