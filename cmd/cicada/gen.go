package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"m31labs.dev/cicada/phrase"
)

func generatorCommand(args []string) error {
	flags := flag.NewFlagSet("gen", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	seed := flags.Uint64("seed", 0, "64-bit generator seed")
	key := flags.String("key", "c", "tonic pitch class")
	scale := flags.String("scale", "minor", "scale")
	rootOctave := flags.Int("root-octave", 2, "root octave")
	steps := flags.Int("steps", 16, "steps per bar")
	density := flags.Float64("density", 0.6, "onset density")
	accentDensity := flags.Float64("accent-density", 0.5, "accent density")
	slideDensity := flags.Float64("slide-density", 0.4, "slide density")
	octaveJump := flags.Float64("octave-jump", 0.3, "octave jump density")
	swing := flags.Float64("swing", 54, "swing percent")
	gate := flags.Int("gate", 55, "gate percent")
	structure := flags.String("structure", "aaba", "A, AABA, ABAB, ABAC, or AAAB")
	restDownbeat := flags.Bool("rest-downbeat", false, "allow a downbeat rest")
	trace := flags.Bool("trace", false, "write draw trace as JSON to stdout")
	output := flags.String("o", "", "output .cicada file; stdout when omitted")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("gen takes flags only")
	}
	if *trace && *output == "" {
		return fmt.Errorf("--trace requires -o so source and trace have separate outputs")
	}
	if *rootOctave < 0 || *rootOctave > 4 || *steps < 0 || *steps > 64 || *gate < 0 || *gate > 100 || math.IsNaN(*swing) || math.IsInf(*swing, 0) || *swing < 50 || *swing > 75 || math.Abs(math.Round(*swing*100)-*swing*100) > 1e-7 {
		return fmt.Errorf("gen parameter is out of range")
	}
	keys := map[string]uint8{"c": 0, "c#": 1, "d": 2, "d#": 3, "e": 4, "f": 5, "f#": 6, "g": 7, "g#": 8, "a": 9, "a#": 10, "b": 11}
	keyValue, ok := keys[strings.ToLower(*key)]
	if !ok {
		return fmt.Errorf("unknown key %q", *key)
	}
	scales := map[string]phrase.Scale{"minor": phrase.Minor, "phrygian": phrase.Phrygian, "dorian": phrase.Dorian, "harmonic": phrase.HarmonicMinor, "pent": phrase.MinorPent, "major": phrase.Major, "mixo": phrase.Mixolydian, "blues": phrase.Blues}
	scaleValue, ok := scales[strings.ToLower(*scale)]
	if !ok {
		return fmt.Errorf("unknown scale %q", *scale)
	}
	structures := map[string]phrase.Structure{"a": phrase.A, "aaba": phrase.AABA, "abab": phrase.ABAB, "abac": phrase.ABAC, "aaab": phrase.AAAB}
	structureValue, ok := structures[strings.ToLower(*structure)]
	if !ok {
		return fmt.Errorf("unknown structure %q", *structure)
	}
	params := phrase.Params{
		Seed: *seed, Key: keyValue, Scale: scaleValue,
		RootOctave: uint8(*rootOctave), Steps: uint8(*steps),
		Density: float32(*density), AccentDensity: float32(*accentDensity),
		SlideDensity: float32(*slideDensity), OctaveJump: float32(*octaveJump),
		SwingPercent100: uint16(math.Round(*swing * 100)), GatePercent: uint8(*gate),
		RestDownbeat: *restDownbeat, Structure: structureValue,
	}
	result, err := phrase.Generate(params)
	if err != nil {
		return fmt.Errorf("CICADA-PARAM: %w", err)
	}
	if *output == "" {
		_, err = fmt.Fprint(os.Stdout, result.Notation)
		return err
	}
	if err := writeNewAtomic(*output, []byte(result.Notation)); err != nil {
		return err
	}
	if *trace {
		return json.NewEncoder(os.Stdout).Encode(result.Trace)
	}
	_, err = fmt.Fprintln(os.Stdout, *output)
	return err
}
