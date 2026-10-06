package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const libUsage = "usage: cicada lib list | show PATH | new PATH [--dir DIR] | update [PATH] | vendor"

func libCommand(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", libUsage)
	}
	if args[0] == "new" {
		return libNewCommand(args[1:], output)
	}
	if len(args) == 2 && args[0] == "show" {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		lib, err := project.InspectLibrary(root, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%s\nresolved: %s (%s)\nhash: sha256:%s\n\nManifest:\n%s", lib.Path, lib.Kind, lib.Root, lib.SHA256, lib.ManifestSource)
		if len(lib.ManifestSource) > 0 && lib.ManifestSource[len(lib.ManifestSource)-1] != '\n' {
			fmt.Fprintln(output)
		}
		fmt.Fprintln(output, "\nDeclarations:")
		var names []string
		for name := range notation.DeclarationNames(lib.Files) {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintln(output, "  "+name)
		}
		fmt.Fprintln(output, "\nAssets:")
		for _, name := range lib.Assets {
			fmt.Fprintln(output, "  "+name)
		}
		return nil
	}
	if len(args) == 1 && args[0] == "list" {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		libraries, err := project.ListLibraries(root)
		if err != nil {
			return err
		}
		for _, library := range libraries {
			fmt.Fprintf(output, "%s\t%s\n", library.Kind, library.Path)
		}
		return nil
	}
	if (args[0] != "update" || len(args) > 2) && (args[0] != "vendor" || len(args) != 1) {
		return fmt.Errorf("%s", libUsage)
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
			if filepath.Clean(current.Root) != filepath.Clean(sources.Root) {
				return fmt.Errorf("CICADA-LIB-ROOT: scores in %q and %q use separate library pins; run cicada lib update from each score directory or add a shared cicada.mod", sources.Root, current.Root)
			}
			sources.Imports = append(sources.Imports, current.Imports...)
			for name, lib := range current.Libraries {
				if sources.Libraries[name] == nil {
					sources.Libraries[name] = lib
					sources.LibraryOrder = append(sources.LibraryOrder, name)
				}
			}
		}
	}
	if args[0] == "vendor" {
		changes, err := sources.VendorLibraries(root)
		if err != nil {
			return err
		}
		for _, change := range changes {
			fmt.Fprintln(output, change)
		}
		if len(changes) == 0 {
			fmt.Fprintln(output, "libraries are already vendored")
		}
		return nil
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
	sum := filepath.Join(sources.Root, "cicada.sum")
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

func libNewCommand(args []string, output io.Writer) error {
	if len(args) != 1 && (len(args) != 3 || args[1] != "--dir") {
		return fmt.Errorf("%s", libUsage)
	}
	name := args[0]
	var dir string
	if len(args) == 3 {
		dir = args[2]
	} else if filepath.IsAbs(name) || filepath.VolumeName(name) != "" || strings.HasPrefix(name, ".") || strings.ContainsRune(name, '\\') {
		dir = name
		name = filepath.Base(filepath.Clean(dir))
	} else {
		if !edition.ValidLibraryPath(name) {
			return fmt.Errorf("CICADA-LIB-PATH: invalid library path")
		}
		base, err := project.UserLibraryDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(base, filepath.FromSlash(name))
	}
	if err := project.NewLibrary(name, dir); err != nil {
		return err
	}
	fmt.Fprintf(output, "created library %s in %s\n", name, dir)
	return nil
}
