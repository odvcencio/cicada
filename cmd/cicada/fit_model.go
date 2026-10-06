package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/modalfit"
	"m31labs.dev/cicada/host/recording"
)

func fitModelCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("fit-model", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "recorded_model", "lowercase instrument name")
	dir := flags.String("o", "", "new pack directory")
	index := flags.Int("hit", 1, "one-based detected hit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 1 {
		return fmt.Errorf("usage: cicada fit-model -o <pack-directory> [--name pencil_model] [--hit 1] <recording.wav>")
	}
	f, err := os.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, recording.MaxInputBytes+1))
	f.Close()
	if err != nil {
		return err
	}
	audio, err := recording.DecodeWAV(data)
	if err != nil {
		return err
	}
	hits, err := recording.Analyze([]recording.Audio{audio}, recording.DefaultOptions())
	if err != nil {
		return err
	}
	if *index < 1 || *index > len(hits) {
		return fmt.Errorf("choose a detected hit from 1–%d", len(hits))
	}
	hit := hits[*index-1]
	model, err := modalfit.Fit(hit.PCM, hit.Rate, hit.SourceSHA256)
	if err != nil {
		return err
	}
	pack, err := model.Build(*name)
	if err != nil {
		return err
	}
	if err = pack.Write(*dir); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%d fitted modes · root %.1f Hz · model sha256 %s · owner recording\n%s", len(model.Modes), model.RootHz, recording.Digest(pack.Files["model.json"]), pack.Source(filepath.Join(*dir, "manifest.json"), model.RootMIDI))
	return err
}
