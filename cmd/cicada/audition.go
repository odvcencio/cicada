package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/scoreaudition"
)

func auditionCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("audition", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "directory for spectrogram.png, chroma.png and rhythm.png")
	from := flags.Int("from", 1, "one-based start bar")
	bars := flags.Int("bars", 0, "bars to analyze; 0 means through song end")
	asJSON := flags.Bool("json", false, "write the report as JSON")
	// Accept the score before or after the flags.
	var path string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		path, rest = rest[0], rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return fmt.Errorf("usage: cicada audition <score.cicada> [-o <dir>] [--from <bar>] [--bars <n>] [--json]")
	}
	if path == "" && len(flags.Args()) == 1 {
		path = flags.Args()[0]
	} else if len(flags.Args()) != 0 || path == "" {
		return fmt.Errorf("usage: cicada audition <score.cicada> [-o <dir>] [--from <bar>] [--bars <n>] [--json]")
	}
	fromBar, err := cliBarOffset(*from, flags, stderr)
	if err != nil {
		return err
	}
	if *bars < 0 {
		return fmt.Errorf("--bars must not be negative")
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
	analysis, err := scoreaudition.Analyze(inspection.score, scoreaudition.Options{From: fromBar, Bars: *bars, AssetDir: inspection.root(path)})
	if err != nil {
		return err
	}
	if *output != "" {
		images, err := analysis.Images()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(*output, 0o755); err != nil {
			return err
		}
		for _, name := range []string{"spectrogram.png", "chroma.png", "rhythm.png"} {
			if err := os.WriteFile(filepath.Join(*output, name), images[name], 0o644); err != nil {
				return err
			}
			analysis.Report.Images = append(analysis.Report.Images, name)
		}
	}
	if *asJSON {
		data, err := json.MarshalIndent(analysis.Report, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, string(data))
		return err
	}
	analysis.Report.WriteText(stdout)
	return nil
}
