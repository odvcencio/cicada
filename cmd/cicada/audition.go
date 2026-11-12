package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/host/scoreaudition"
	"m31labs.dev/cicada/render"
)

const auditionUsage = "usage: cicada audition <score.cicada> [-o <dir>] [--from <bar>] [--bars <n>] [--tracks <a,b>] [--peaks <n>] [--detail] [--no-bands] [--image-width <px>] [--json] [--max-resident-pcm <size>]"

// auditionErrorDocument is what audition --json prints when it fails.
type auditionErrorDocument struct {
	Schema string            `json:"schema"`
	Format string            `json:"format"`
	Error  auditionErrorJSON `json:"error"`
}

// auditionErrorJSON is jsonError with, for unknown_track, the score's track
// names.
type auditionErrorJSON struct {
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	ValidTracks []string `json:"valid_tracks,omitempty"`
}

// splitTrackList reads a --tracks value: names separated by commas.
func splitTrackList(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, name := range strings.Split(value, ",") {
		out = append(out, strings.TrimSpace(name))
	}
	return out
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
		document := auditionErrorDocument{Schema: scoreaudition.Schema, Format: scoreaudition.FormatVersion, Error: auditionErrorJSON{Code: code, Message: err.Error()}}
		var unknown *render.UnknownTrackError
		if errors.As(err, &unknown) {
			document.Error.Code, document.Error.ValidTracks = "unknown_track", unknown.Valid
		}
		if writeErr := writeAgentJSON(stdout, document); writeErr != nil {
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
	output := flags.String("o", "", "directory for spectrogram.png, chroma.png, rhythm.png, energy.png and pages")
	from := flags.Int("from", 1, "one-based start bar")
	bars := flags.Int("bars", 0, "bars to analyze; 0 means through song end")
	asJSON := flags.Bool("json", false, "write the report as JSON")
	tracks := flags.String("tracks", "", "analyze only these tracks (comma-separated) and the mix")
	peaks := flags.Int("peaks", scoreaudition.DefaultPeaks, "true-peak moments to list; 0 lists none")
	imageWidth := flags.Int("image-width", scoreaudition.DefaultImageWidth, "widest picture in pixels; 0 keeps one full strip")
	detail := flags.Bool("detail", false, "add the per-bar and per-beat matrices to the JSON")
	noBands := flags.Bool("no-bands", false, "skip band ownership, about an eighth of the CPU time")
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
	if *peaks < 0 || *peaks > 100 {
		return &codedError{"usage", fmt.Errorf("--peaks must be 0 to 100")}
	}
	if *imageWidth < 0 || *imageWidth > 0 && *imageWidth < 200 {
		return &codedError{"usage", fmt.Errorf("--image-width must be 0 (no cap) or at least 200")}
	}
	if err := render.CheckTrackNames(splitTrackList(*tracks), splitTrackList(*tracks)); err != nil {
		return &codedError{"usage", fmt.Errorf("--tracks: %w", err)}
	}
	peakCount := *peaks
	if peakCount == 0 {
		peakCount = -1
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
	analysis, err := scoreaudition.Analyze(inspection.score, scoreaudition.Options{From: fromBar, Bars: *bars, AssetDir: inspection.root(path), MaxResidentPCM: residentPCM, Tracks: splitTrackList(*tracks), Peaks: peakCount, NoBands: *noBands})
	if err != nil {
		var unknown *render.UnknownTrackError
		if errors.As(err, &unknown) {
			return &codedError{"unknown_track", err}
		}
		var list *render.TrackListError
		if errors.As(err, &list) {
			return &codedError{"usage", err}
		}
		return err
	}
	if *output != "" {
		images, pictures, err := analysis.Pictures(*imageWidth)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(*output, 0o755); err != nil {
			return err
		}
		for _, picture := range pictures {
			if err := os.WriteFile(filepath.Join(*output, picture.File), images[picture.File], 0o644); err != nil {
				return err
			}
			switch picture.File {
			case "spectrogram.png", "chroma.png", "rhythm.png":
				analysis.Report.Images = append(analysis.Report.Images, picture.File)
			}
		}
		analysis.Report.Pictures = pictures
	}
	if *asJSON {
		if !*detail {
			analysis.Report.DropDetail()
		}
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
