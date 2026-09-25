package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/notation"
)

type projectFormatEdit struct {
	path      string
	before    []byte
	formatted []byte
}

// formatProjectCommand formats the nearest Cicada project, or the current
// directory when no manifest exists. It prepares every score before writing
// any, so a malformed score cannot leave a partly formatted project.
func formatProjectCommand(check bool, stdout, stderr io.Writer) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	return formatProject(root, check, stdout, stderr)
}

func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	_, manifest, err := scoreEdition(filepath.Join(cwd, "main.cicada"))
	if err != nil {
		return "", err
	}
	if manifest != "" {
		return filepath.Dir(manifest), nil
	}
	return cwd, nil
}

func formatProject(root string, check bool, stdout, stderr io.Writer) error {
	edits, err := collectProjectFormatEdits(root)
	if err != nil {
		return err
	}
	if check {
		for _, edit := range edits {
			fmt.Fprintf(stderr, "--- %s\n+++ %s (formatted)\n%s", edit.path, edit.path, simpleDiff(edit.before, edit.formatted))
		}
		if len(edits) != 0 {
			return fmt.Errorf("%d score(s) need formatting", len(edits))
		}
		return nil
	}
	for _, edit := range edits {
		if err := writeAtomic(edit.path, edit.formatted); err != nil {
			return fmt.Errorf("%s: %w", edit.path, err)
		}
		fmt.Fprintln(stdout, edit.path)
	}
	return nil
}

func collectProjectFormatEdits(root string) ([]projectFormatEdit, error) {
	paths, err := projectScorePaths(root)
	if err != nil {
		return nil, err
	}
	var edits []projectFormatEdit
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		document, err := notation.ParseDocument(source)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		formatted, err := notation.Format(document)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !bytes.Equal(source, formatted) {
			edits = append(edits, projectFormatEdit{path: path, before: source, formatted: formatted})
		}
	}
	return edits, nil
}

func projectScorePaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "cicada.mod")); err == nil {
				return filepath.SkipDir
			} else if !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		if filepath.Ext(path) != ".cicada" || !entry.Type().IsRegular() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}
