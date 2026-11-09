package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"m31labs.dev/cicada/host/gamescore"
	"m31labs.dev/cicada/sdk/gosxhost"
)

// gameScoreCommand compiles a score with a live block into a score pack for a
// GoSX game: the kernel image and the Game Director manifest in one file, at
// one sample rate. The game ships the pack and one kernel module; no compiler
// runs in the browser.
func gameScoreCommand(args []string, stdout, stderr io.Writer) error {
	errUsage := errors.New("usage: cicada game-score <score.cicada> -o <score.cscore> [--rate <Hz>]")
	flags := flag.NewFlagSet("game-score", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "output score pack")
	rate := flags.Int("rate", portableRate, "sample rate")
	var scores []string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		scores, rest = []string{rest[0]}, rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return errUsage
	}
	scores = append(scores, flags.Args()...)
	if len(scores) != 1 || filepath.Ext(scores[0]) != ".cicada" || filepath.Ext(*output) != ".cscore" {
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
	root, err := studioProjectRoot(path)
	if err != nil {
		return err
	}
	pack, err := gamescore.Build(inspection.semantic, root, *rate)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	surface, image := pack.Surface, pack.Image
	data, err := pack.Marshal()
	if err != nil {
		return err
	}
	if err := writeNewAtomic(*output, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s: %d Hz, image %d bytes, states %d, macros %d, stingers %d; needs %s\n",
		*output, *rate, len(image), len(surface.States), len(surface.Macros), len(surface.Stingers), gosxhost.KernelName(pack.Kernel))
	return err
}
