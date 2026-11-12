package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/scoreaudition"
)

const auditionUsage = "usage: cicada audition <score.cicada> [-o <dir>] [--from <bar>] [--bars <n>] [--json] [--max-resident-pcm <size>] [--jobs auto|<n>]"

// auditionErrorDocument is what audition --json prints when it fails.
type auditionErrorDocument struct {
	Schema string    `json:"schema"`
	Format string    `json:"format"`
	Error  jsonError `json:"error"`
}

// codedError is an error with the stable code its --json document carries.
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func auditionCommand(args []string, stdout, stderr io.Writer) error {
	err := auditionRun(args, stdout, stderr)
	if err != nil && hasJSONFlag(args) {
		code := "audition_failed"
		var coded *codedError
		if errors.As(err, &coded) {
			code = coded.code
		}
		if writeErr := writeAgentJSON(stdout, auditionErrorDocument{Schema: scoreaudition.Schema, Format: scoreaudition.FormatVersion, Error: jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func auditionRun(args []string, stdout, stderr io.Writer) error {
	args, residentPCM, err := parseMaxResidentPCM(args)
	if err != nil {
		return &codedError{"usage", err}
	}
	flags := flag.NewFlagSet("audition", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "directory for spectrogram.png, chroma.png and rhythm.png")
	from := flags.Int("from", 1, "one-based start bar")
	bars := flags.Int("bars", 0, "bars to analyze; 0 means through song end")
	asJSON := flags.Bool("json", false, "write the report as JSON")
	jobsText := flags.String("jobs", "", "render lanes: auto or a count; absent renders serially")
	// Accept the score before or after the flags.
	var path string
	rest := args
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		path, rest = rest[0], rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return &codedError{"usage", fmt.Errorf("%s", auditionUsage)}
	}
	if path == "" && len(flags.Args()) == 1 {
		path = flags.Args()[0]
	} else if len(flags.Args()) != 0 || path == "" {
		return &codedError{"usage", fmt.Errorf("%s", auditionUsage)}
	}
	fromBar, err := cliBarOffset(*from, flags, stderr)
	if err != nil {
		return &codedError{"usage", err}
	}
	if *bars < 0 {
		return &codedError{"usage", fmt.Errorf("--bars must not be negative")}
	}
	jobs, err := parseJobs(*jobsText)
	if err != nil {
		return &codedError{"usage", err}
	}
	inspection, err := inspectScore(path)
	if err != nil {
		return &codedError{"unreadable_score", err}
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
		return &codedError{"invalid_score", fmt.Errorf("%s has errors", path)}
	}
	analysis, err := scoreaudition.Analyze(inspection.score, scoreaudition.Options{From: fromBar, Bars: *bars, AssetDir: inspection.root(path), MaxResidentPCM: residentPCM, Jobs: jobs})
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
