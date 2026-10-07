// cicada-keys-pack renders owned modeled keyboards to the pinned sampler format.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cicada-keys-pack:", err)
		os.Exit(1)
	}
}

func run(args []string, output, errors io.Writer) error {
	options := defaults()
	flags := flag.NewFlagSet("cicada-keys-pack", flag.ContinueOnError)
	flags.SetOutput(errors)
	flags.StringVar(&options.Out, "out", "", "output directory; each patch gets a separate pack directory")
	flags.StringVar(&options.Patch, "patch", options.Patch, "modeled patch name or all")
	flags.IntVar(&options.Step, "step", options.Step, "root note interval: 1 (chromatic) or 3 (minor thirds)")
	flags.IntVar(&options.Layers, "layers", options.Layers, "velocity layers, 5..16")
	flags.IntVar(&options.RoundRobins, "round-robins", options.RoundRobins, "takes per root and layer, 2..32")
	flags.IntVar(&options.Low, "low", options.Low, "lowest MIDI note, 21..108")
	flags.IntVar(&options.High, "high", options.High, "highest MIDI note, 21..108")
	flags.Float64Var(&options.Duration, "duration", options.Duration, "held capture seconds; zero selects 4 seconds mono or 2 seconds stereo")
	flags.IntVar(&options.Rate, "rate", options.Rate, "output sample rate; 48000")
	flags.BoolVar(&options.EstimateOnly, "estimate-only", false, "validate dimensions and report decoded PCM sizes without writing packs")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	plans, err := planPacks(options)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		fmt.Fprintf(output, "%s: %d roots, %d velocity layers, %d round robins, %d channels, %.3f seconds, decoded PCM %d/%d bytes\n", plan.Patch, len(plan.Roots), len(plan.Layers), options.RoundRobins, plan.Channels, float64(plan.Frames)/float64(options.Rate), plan.PCMBytes, maxPCMBytes)
	}
	if options.EstimateOnly {
		return nil
	}
	if options.Out == "" {
		return fmt.Errorf("--out is required")
	}
	if err := preflightOutput(options.Out, plans); err != nil {
		return err
	}
	filter := newDecimator()
	for _, plan := range plans {
		summary, err := buildPack(options, plan, filter, output)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%s: %d assets, %d zones, manifest SHA256 %s\n", plan.Patch, summary.Assets, summary.Zones, summary.ManifestSHA256)
	}
	return nil
}
