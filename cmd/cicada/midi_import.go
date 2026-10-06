package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/smf"
)

func importMIDICommand(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cicada import-midi <file.mid> -o <score.cicada>")
	}
	flags := flag.NewFlagSet("import-midi", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "new score path")
	quantize := flags.String("quantize", "", "explicit onset grid")
	reportJSON := flags.Bool("report", false, "JSON report")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || *output == "" || filepath.Ext(*output) != ".cicada" {
		return fmt.Errorf("usage: cicada import-midi <file.mid> -o <score.cicada> [--quantize 1/8t] [--report]")
	}
	var options smf.ImportOptions
	if *quantize != "" {
		grid, err := notation.GridTicks(*quantize)
		if err != nil {
			return err
		}
		options.GridTicks = grid
	}
	input, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer input.Close()
	file, err := smf.Decode(input)
	if err != nil {
		return err
	}
	source, report, err := smf.ImportSource(file, options)
	if err != nil {
		return err
	}
	if err := writeImportedScore(*output, source); err != nil {
		return err
	}

	if *reportJSON {
		return json.NewEncoder(os.Stderr).Encode(report)
	}
	fmt.Printf("%s: imported %d notes, %d tracks, %d bars on a %d-tick grid; %d onset edits, %d duration edits, %d cross-bar retriggers, %d drum velocity edits\n", *output, report.Notes, report.Tracks, report.Bars, report.GridTicks, report.QuantizedOnsets, report.QuantizedDurations, report.SplitNotes, report.QuantizedVelocities)
	return nil
}

// Link the complete temporary file atomically, without replacing a destination.
func writeImportedScore(path string, source []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cicada-midi-import-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0644); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(source); err != nil {
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
	return os.Link(temporary.Name(), path)
}
