package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/smf"
)

type midiOptions struct {
	output, pattern string
	bars            int
	report          bool
}

func parseMIDIArgs(args []string) midiOptions {
	flags := flag.NewFlagSet("midi", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "output Standard MIDI File")
	bars := flags.Int("bars", 0, "song bars; zero means all")
	pattern := flags.String("pattern", "", "export only this pattern")
	report := flags.Bool("report", false, "write JSON report to stderr")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 || *output == "" || *bars < 0 || *pattern != "" && *bars != 0 {
		usage()
	}
	return midiOptions{output: *output, bars: *bars, pattern: *pattern, report: *report}
}

func midiFile(p *project.Project, opts midiOptions) error {
	var file smf.File
	var err error
	if opts.pattern != "" {
		file, err = smf.FromPattern(p, opts.pattern)
	} else {
		file, err = smf.FromProject(p, opts.bars)
	}
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(opts.output), ".cicada-midi-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if info, err := os.Stat(opts.output); err == nil {
		if err := temporary.Chmod(info.Mode().Perm()); err != nil {
			temporary.Close()
			return err
		}
	} else if !os.IsNotExist(err) {
		temporary.Close()
		return err
	}
	encodeErr := smf.Encode(file, temporary)
	if encodeErr == nil {
		encodeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporary.Name(), opts.output); err != nil {
		return err
	}
	notes := 0
	for _, track := range file.Tracks {
		notes += len(track.Notes)
	}
	if opts.report {
		return json.NewEncoder(os.Stderr).Encode(map[string]any{
			"path": opts.output, "format": file.Format, "ppq": file.PPQ,
			"tracks": len(file.Tracks), "notes": notes, "tempo_micros": file.TempoMicros,
		})
	}
	fmt.Printf("%s: SMF type 1, %d PPQ, %d tracks, %d notes\n", opts.output, file.PPQ, len(file.Tracks), notes)
	return nil
}

func verifyMIDICommand(args []string) error {
	if len(args) < 1 {
		usage()
	}
	flags := flag.NewFlagSet("verify-midi", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ppq := flags.Int("ppq", 960, "expected pulses per quarter note")
	typeID := flags.Int("type", 1, "expected SMF type")
	report := flags.Bool("report", false, "write JSON report to stderr")
	if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 || *ppq < 1 || *ppq > 32767 || *typeID < 0 || *typeID > 1 {
		usage()
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
	var reencoded bytes.Buffer
	if err := smf.Encode(file, &reencoded); err != nil {
		return fmt.Errorf("MIDI round trip encode: %w", err)
	}
	decodedAgain, err := smf.Decode(bytes.NewReader(reencoded.Bytes()))
	if err != nil {
		return fmt.Errorf("MIDI round trip decode: %w", err)
	}
	if err := smf.MusicalDiff(file, decodedAgain); err != nil {
		return fmt.Errorf("MIDI event round trip: %w", err)
	}
	if file.Format != uint16(*typeID) || file.PPQ != uint16(*ppq) || file.TempoMicros == 0 {
		return fmt.Errorf("MIDI format, PPQ, or tempo differs from request")
	}
	if *typeID == 1 {
		if len(file.Tracks) < 2 {
			return fmt.Errorf("type 1 MIDI requires conductor and instrument tracks")
		}
		var tempo, meter, key bool
		for _, event := range file.Tracks[0].Meta {
			switch event.Type {
			case 0x51:
				tempo = true
			case 0x58:
				meter = true
			case 0x59:
				key = true
			}
		}
		if !tempo || !meter || !key {
			return fmt.Errorf("conductor track lacks tempo, time signature, or key signature")
		}
	}
	notes := 0
	for _, track := range file.Tracks {
		notes += len(track.Notes)
	}
	if *report {
		return json.NewEncoder(os.Stderr).Encode(map[string]any{
			"path": args[0], "format": file.Format, "ppq": file.PPQ,
			"tracks": len(file.Tracks), "notes": notes, "tempo_micros": file.TempoMicros,
		})
	}
	fmt.Printf("%s: SMF type %d, %d PPQ, %d tracks, %d notes, tempo %d us/quarter\n", args[0], file.Format, file.PPQ, len(file.Tracks), notes, file.TempoMicros)
	return nil
}

func compareMIDICommand(args []string) error {
	if len(args) != 2 {
		usage()
	}
	read := func(path string) (smf.File, error) {
		file, err := os.Open(path)
		if err != nil {
			return smf.File{}, err
		}
		defer file.Close()
		return smf.Decode(file)
	}
	left, err := read(args[0])
	if err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	right, err := read(args[1])
	if err != nil {
		return fmt.Errorf("%s: %w", args[1], err)
	}
	if err := smf.MusicalDiff(left, right); err != nil {
		return err
	}
	fmt.Println("equal")
	return nil
}
