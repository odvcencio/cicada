package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ed "m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/migration"
)

// fixCommand performs the first edition-1 migrations without reprinting the
// score: it moves a legacy header to a manifest, makes instrument registers
// explicit, and updates chance and statement separators. Comments, phrase
// spelling, and layout stay in place.
func fixCommand(args []string) error {
	return fixCommandWithWriter(args, writeFixedScore)
}

func fixCommandWithWriter(args []string, writeScore func(string, []byte, os.FileMode) error) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: cicada fix <score.cicada> [--check]")
	}
	check := false
	if len(args) == 2 {
		if args[0] == "--check" {
			args[0], args[1] = args[1], args[0]
		}
		if args[1] != "--check" {
			return fmt.Errorf("usage: cicada fix <score.cicada> [--check]")
		}
		check = true
	}
	path := args[0]
	if filepath.Ext(path) != ".cicada" {
		return fmt.Errorf("fix needs a .cicada score")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	edition, manifest, err := scoreEdition(path)
	if err != nil {
		return err
	}
	if edition != 1 {
		return fmt.Errorf("CICADA-VERSION: only cicada 1 is supported")
	}
	fixed, changed, err := fixSource(source)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	newManifest := manifest == ""
	if !changed && !newManifest {
		fmt.Println("already fixed:", path)
		return nil
	}
	if check {
		return fmt.Errorf("fix needed: %s (score changed: %t, manifest needed: %t)", path, changed, newManifest)
	}
	var mode os.FileMode
	if changed {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	}
	createdManifest := false
	complete := false
	defer func() {
		if createdManifest && !complete {
			_ = os.Remove(manifest)
		}
	}()
	if newManifest {
		name := strings.TrimSuffix(filepath.Base(path), ".cicada")
		if !ed.ValidProjectName(name) {
			return fmt.Errorf("cannot derive project name from %q", path)
		}
		manifest = filepath.Join(filepath.Dir(path), "cicada.mod")
		if err := os.WriteFile(manifest, []byte("project "+name+"\ncicada 1\n"), 0644); err != nil {
			return err
		}
		createdManifest = true
	}
	if changed {
		if err := writeScore(path, fixed, mode); err != nil {
			return err
		}
	}
	complete = true
	fmt.Printf("fixed %s; edition manifest %s\n", path, manifest)
	return nil
}

func writeFixedScore(path string, source []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".cicada-fix-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(source); err != nil {
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
	return os.Rename(file.Name(), path)
}

func fixSource(source []byte) ([]byte, bool, error) {
	return migration.FixSource(source)
}
