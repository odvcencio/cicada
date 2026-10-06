package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"m31labs.dev/cicada/host/wam2"
	"m31labs.dev/cicada/project"
)

func wam2Command(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cicada wam2 <score.cicada> -o <directory>")
	}
	flags := flag.NewFlagSet("wam2", flag.ContinueOnError)
	output := flags.String("o", "", "output plugin directory")
	kernel := flags.String("kernel", "build/cicada-kernel.wasm", "kernel WASM path")
	sdk := flags.String("sdk", "build/wam2-sdk/node_modules/@webaudiomodules/sdk", "WAM SDK directory")
	track := flags.String("midi-track", "", "MIDI instrument track (default first supported track)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected WAM2 export arguments")
	}
	score, diagnostics, err := project.LoadScore(args[0], nil)
	if err != nil {
		return err
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return fmt.Errorf("%s: %s", d.Code, d.Message)
		}
	}
	p, diagnostics := project.FromScore(score)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return fmt.Errorf("%s: %s", d.Code, d.Message)
		}
	}
	if err := wam2.Export(p, filepath.Dir(args[0]), *output, wam2.Options{KernelPath: *kernel, SDKPath: *sdk, MIDITrack: *track}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Exported WAM2 instrument. Serve the directory over HTTP and import index.js in a WAM2 host.")
	return nil
}
