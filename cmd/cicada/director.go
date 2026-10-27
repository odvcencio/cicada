package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/project"
)

// directorCommand writes the Game Director manifest for a score: its live
// block resolved to kernel IDs, as project.DirectorSurfaceOf builds it. Browser
// hosts and the @cicada/game-director package read this file.
func directorCommand(args []string, stdout, stderr io.Writer) error {
	errUsage := errors.New("usage: cicada director <score.cicada> -o <director.json> [--rate <Hz>]")
	flags := flag.NewFlagSet("director", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "output manifest")
	rate := flags.Int("rate", portableRate, "sample rate")
	// Accept the score before or after the flags.
	var scores []string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		scores, rest = []string{rest[0]}, rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return errUsage
	}
	scores = append(scores, flags.Args()...)
	if len(scores) != 1 || filepath.Ext(scores[0]) != ".cicada" || filepath.Ext(*output) != ".json" {
		return errUsage
	}
	path := scores[0]
	if !slices.Contains(portableRates, *rate) {
		return fmt.Errorf("--rate must be 44100, 48000 or 96000")
	}
	inspection, err := inspectScore(path)
	if err != nil {
		return err
	}
	failed := false
	for _, d := range inspection.diagnostics {
		if d.Position.File == "" {
			d.Position.File = path
		}
		fmt.Fprintln(stderr, d.Error())
		failed = failed || d.Severity == "error"
	}
	if failed {
		return fmt.Errorf("%s has errors", path)
	}
	// The browser kernel runs 16 tracks, so a manifest names at most 16. The
	// surface check folds this limit into a message that does not name it.
	if tracks := len(inspection.semantic.Tracks); tracks > limits.PortableMaxTracks {
		return fmt.Errorf("%s: a Game Director manifest supports at most %d tracks, and this score has %d; remove or merge tracks (cicada check --profile browser lists them)", path, limits.PortableMaxTracks, tracks)
	}
	surface, err := project.DirectorSurfaceOf(inspection.semantic, *rate)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	data, err := json.MarshalIndent(surface, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewAtomic(*output, append(data, '\n')); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s: %d Hz, states %d, macros %d, stingers %d, transitions %d\n", *output, *rate, len(surface.States), len(surface.Macros), len(surface.Stingers), len(surface.Transitions))
	return err
}
