package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"m31labs.dev/cicada/host/recording"
)

const recordKitUsage = "usage: cicada record-kit -o <pack-directory> [--name kit] <recipe.json>"

func recordKitCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("record-kit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "kit", "lowercase kit name")
	dir := flags.String("o", "", "new pack directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() == 0 {
		return fmt.Errorf(recordKitUsage)
	}
	if flags.NArg() > 1 {
		if strings.HasPrefix(flags.Arg(1), "-") {
			return fmt.Errorf("put flags before the recipe\n%s", recordKitUsage)
		}
		return fmt.Errorf("give one recipe file\n%s", recordKitUsage)
	}
	path := flags.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, recording.MaxKitRecipeBytes+1))
	f.Close()
	if err != nil {
		return err
	}
	recipe, err := recording.ParseKitRecipe(data)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	// Takes are looked up in the recipe's own folder.
	pack, report, err := recording.BuildKit(*name, recipe, os.DirFS(filepath.Dir(path)))
	if err != nil {
		return err
	}
	if err = pack.Write(*dir); err != nil {
		return err
	}
	fmt.Fprintf(output, "%d pieces · %d hits · %d zones · owner recording\n", len(report.Pieces), report.Hits, report.Zones)
	table := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "piece\tnote\thits\tlayers (hits at first-40-ms level, dBFS)\tchoke")
	for _, piece := range report.Pieces {
		layers := make([]string, len(piece.Layers))
		for i, layer := range piece.Layers {
			layers[i] = fmt.Sprintf("%d at %.0f..%.0f", layer.Hits, layer.MinDB, layer.MaxDB)
		}
		choke := "-"
		switch {
		case piece.Choke != "":
			choke = piece.Choke
		case piece.ChokeNote != 0:
			choke = fmt.Sprintf("note %d (%s)", piece.ChokeNote, recording.NoteName(piece.ChokeNote))
		}
		fmt.Fprintf(table, "%s\t%s %d\t%d\t%s\t%s\n", piece.Name, recording.NoteName(piece.Note), piece.Note, piece.Hits, strings.Join(layers, " · "), choke)
	}
	if err = table.Flush(); err != nil {
		return err
	}
	for _, warning := range report.Warnings() {
		fmt.Fprintf(output, "warning: %s\n", warning)
	}
	_, err = fmt.Fprintf(output, "%sstarter score: %s\n", pack.Source(filepath.Join(*dir, "manifest.json"), recipe.Pieces[0].Note), filepath.Join(*dir, "instrument.cicada"))
	return err
}
