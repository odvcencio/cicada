package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/project"
)

func libCommand(args []string, output io.Writer) error {
	if len(args) < 1 || args[0] != "update" || len(args) > 2 {
		return fmt.Errorf("usage: cicada lib update [PATH]")
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	paths, err := projectScorePaths(root)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("project has no scores")
	}
	var sources *project.Sources
	// Legacy manifests load independent scores. Their imports share one sum.
	for _, path := range paths {
		current, err := project.ReadSources(path, nil)
		if err != nil {
			return err
		}
		if sources == nil {
			sources = current
		} else {
			for name, lib := range current.Libraries {
				if sources.Libraries[name] == nil {
					sources.Libraries[name] = lib
					sources.LibraryOrder = append(sources.LibraryOrder, name)
				}
			}
		}
	}
	name := ""
	if len(args) == 2 {
		name = args[1]
		if !edition.ValidLibraryPath(name) {
			return fmt.Errorf("CICADA-LIB-PATH: invalid library path")
		}
	}
	data, changes, err := sources.UpdateLibraries(name)
	if err != nil {
		return err
	}
	sum := filepath.Join(root, "cicada.sum")
	if len(changes) != 0 {
		if err := writeNewAtomic(sum, data); err != nil {
			return err
		}
		for _, change := range changes {
			fmt.Fprintln(output, change)
		}
	} else if _, err := os.Stat(sum); os.IsNotExist(err) {
		if err := writeNewAtomic(sum, data); err != nil {
			return err
		}
	}
	if len(changes) == 0 {
		fmt.Fprintln(output, "library pins are unchanged")
	}
	return nil
}
