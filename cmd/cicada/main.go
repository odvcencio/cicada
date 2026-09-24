// Command cicada provides the first grammar-backed language tools.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "convert" || os.Args[1] == "compare") {
		projectCommand(os.Args[1:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fmt" {
		formatCommand(os.Args[2:])
		return
	}
	if len(os.Args) < 3 || len(os.Args) > 5 {
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
	if command == "render" && (len(os.Args) != 5 || os.Args[3] != "-o") {
		usage()
	}
	if command != "validate" && command != "ast" && command != "events" && command != "graph" && command != "render" {
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
		var compileDiagnostics []notation.Diagnostic
		programs, compileDiagnostics = project.Check(score)
		diagnostics = append(diagnostics, compileDiagnostics...)
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
		if err := renderFile(score, os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cicada validate|ast <file.cicada> | events <file.cicada> <track> <pattern> | graph <file.cicada> <instrument> | render <file.cicada> -o <out.wav> | fmt [--check|-w] <file.cicada> | convert <in> -o <out> | compare --semantic <a> <b>")
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

func renderFile(score *notation.Score, path string) error {
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
	report, renderErr := render.WAV(score, render.Options{SampleRate: 48_000, TailSec: 1}, file)
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
	fmt.Printf("%s: %d bars, %d frames at %d Hz, peak %.3f\n", path, report.Bars, report.Frames, report.SampleRate, report.Peak)
	return nil
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
