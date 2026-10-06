package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/recording"
)

func recordPackCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("record-pack", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := recording.DefaultOptions()
	name := flags.String("name", "recorded", "lowercase instrument name")
	dir := flags.String("o", "", "new pack directory")
	flags.IntVar(&options.Root, "root", 60, "fallback MIDI root")
	flags.IntVar(&options.Layers, "layers", 3, "velocity layers")
	flags.BoolVar(&options.AutoPitch, "auto-pitch", false, "map detected note roots")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() == 0 {
		return fmt.Errorf("usage: cicada record-pack -o <pack-directory> [--name recorded] [--root 60] [--layers 3] <recording.wav>...")
	}
	inputs := make([]recording.Audio, 0, flags.NArg())
	if flags.NArg() > 32 {
		return fmt.Errorf("choose at most 32 input WAV files")
	}
	for _, path := range flags.Args() {
		f, err := os.Open(path)
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
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		inputs = append(inputs, audio)
	}
	hits, err := recording.Analyze(inputs, options)
	if err != nil {
		return err
	}
	pack, err := recording.Build(*name, hits, options.Layers)
	if err != nil {
		return err
	}
	if err = pack.Write(*dir); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%d hits · %d zones · user recording\n%s", len(hits), len(pack.Manifest.Zones), pack.Source(filepath.Join(*dir, "manifest.json"), options.Root))
	return err
}
