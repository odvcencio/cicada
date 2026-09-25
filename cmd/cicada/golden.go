package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func goldenCommand(args []string) error {
	flags := flag.NewFlagSet("golden", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	update := flags.Bool("update", false, "write the golden fingerprint")
	scorePath := flags.String("score", "examples/first-acid.cicada", "Cicada score")
	goldenPath := flags.String("out", "testdata/golden/first-acid.fp", "fingerprint fixture")
	rate := flags.Int("rate", 48_000, "sample rate")
	bars := flags.Int("bars", 8, "bars to render")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return fmt.Errorf("usage: cicada golden [--update] [--score file.cicada] [--out file.fp] [--rate 48000] [--bars 8]")
	}
	source, err := os.ReadFile(*scorePath)
	if err != nil {
		return err
	}
	if err := checkScoreEdition(*scorePath); err != nil {
		return err
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return fmt.Errorf("%s:%d:%d: %s", *scorePath, diagnostic.Position.Line, diagnostic.Position.Column, diagnostic.Message)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		return fmt.Errorf("score cannot be compiled: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, *rate, 128)
	if err != nil {
		return err
	}
	current, err := render.EngineFingerprint(cfg, *bars)
	if err != nil {
		return err
	}
	if *update {
		if previousData, err := os.ReadFile(*goldenPath); err == nil {
			previous, err := render.DecodeFingerprint(previousData)
			if err != nil {
				return err
			}
			diff, err := render.FingerprintDrift(previous, current)
			if err != nil {
				return err
			}
			fmt.Printf("previous fingerprint drift: mean %.3f dB, maximum %.3f dB, first-sample hash changed=%v\n", diff.MeanDB, diff.MaxDB, previous.FirstSamplesHash != current.FirstSamplesHash)
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := current.MarshalBinary()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*goldenPath), 0755); err != nil {
			return err
		}
		file, err := os.CreateTemp(filepath.Dir(*goldenPath), ".cicada-golden-*")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		if err := file.Chmod(0644); err != nil {
			file.Close()
			return err
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := os.Rename(file.Name(), *goldenPath); err != nil {
			return err
		}
		fmt.Printf("updated %s: %d samples, %d spectral frames, %d bands\n", *goldenPath, current.Samples, len(current.Frames), render.FingerprintBands)
		return nil
	}
	data, err := os.ReadFile(*goldenPath)
	if err != nil {
		return err
	}
	golden, err := render.DecodeFingerprint(data)
	if err != nil {
		return err
	}
	diff, err := render.CompareFingerprint(golden, current)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d samples, %d frames, %d bands, mean %.3f dB, maximum %.3f dB\n", *goldenPath, current.Samples, len(current.Frames), render.FingerprintBands, diff.MeanDB, diff.MaxDB)
	fmt.Println("PASS test-golden")
	return nil
}
