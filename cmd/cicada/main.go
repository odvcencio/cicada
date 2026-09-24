// Command cicada provides the first grammar-backed language tools.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func main() {
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
	programs := make(map[string]*instrument.Program)
	if score != nil {
		for _, definition := range score.Instruments {
			program, ds := instrument.Compile(definition)
			diagnostics = append(diagnostics, ds...)
			if program != nil {
				programs[definition.Name] = program
			}
		}
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
		if err := renderFile(score, os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cicada validate|ast <file.cicada> | events <file.cicada> <track> <pattern> | graph <file.cicada> <instrument> | render <file.cicada> -o <out.wav>")
	os.Exit(2)
}

func renderFile(score *notation.Score, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	report, renderErr := render.WAV(score, render.Options{SampleRate: 48_000, TailSec: 1}, file)
	closeErr := file.Close()
	if renderErr != nil {
		os.Remove(path)
		return renderErr
	}
	if closeErr != nil {
		return closeErr
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
