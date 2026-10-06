package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/host/transcription"
)

func transcribeCommand(args []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("transcribe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := transcription.DefaultOptions()
	flags.Float64Var(&options.Tempo, "tempo", 0, "tempo in BPM, 0 infers it")
	flags.StringVar(&options.Key, "key", "auto", "auto or tonic major/minor")
	grid := flags.String("grid", "1/16", "1/4, 1/8 or 1/16")
	destination := flags.String("o", "", "write a new score file")
	report := flags.Bool("report", false, "write measured notes and confidence JSON to stderr")
	path := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if path == "" && flags.NArg() == 1 {
		path = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return fmt.Errorf("choose one WAV recording")
	}
	if path == "" {
		return fmt.Errorf("usage: cicada transcribe <recording.wav> [--tempo BPM] [--key 'c major'] [--grid 1/16] [-o melody.cicada] [--report]")
	}
	if !strings.HasPrefix(*grid, "1/") {
		return fmt.Errorf("grid must be 1/4, 1/8 or 1/16")
	}
	var err error
	options.Grid, err = strconv.Atoi(strings.TrimPrefix(*grid, "1/"))
	if err != nil || options.Grid != 4 && options.Grid != 8 && options.Grid != 16 {
		return fmt.Errorf("grid must be 1/4, 1/8 or 1/16")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, recording.MaxInputBytes+1))
	f.Close()
	if err != nil {
		return err
	}
	result, err := transcription.DecodeWAV(data, options)
	if err != nil {
		return err
	}
	result, err = transcription.Complete(result, options)
	if err != nil {
		return err
	}
	if *destination == "" {
		_, err = io.WriteString(output, result.Source)
	} else {
		var file *os.File
		file, err = os.OpenFile(*destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = io.WriteString(file, result.Source)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				_ = os.Remove(*destination)
			}
		}
	}
	if err != nil {
		return err
	}
	if *report {
		return json.NewEncoder(diagnostics).Encode(result)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(diagnostics, warning)
	}
	return nil
}
