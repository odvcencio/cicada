package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type sourceEdit struct {
	start, end int
	text       string
}

// fixCommand performs the first edition-1 migrations without reprinting the
// score: it moves a legacy header to a manifest and makes instrument registers
// explicit. Comments, phrase spelling, and layout stay in place.
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
		if !projectName.MatchString(name) {
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
	before, diagnostics := notation.Parse(source)
	if hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("score must validate before fix: %+v", diagnostics)
	}
	projectBefore, diagnostics := project.FromScore(before)
	if projectBefore == nil || hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("score must compile before fix: %+v", diagnostics)
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, false, err
	}
	var edits []sourceEdit
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		switch walker.Type(node) {
		case "integer":
			start := bytes.LastIndexByte(source[:node.StartByte()], '\n') + 1
			end := int(node.EndByte())
			for end < len(source) && source[end] != '\n' {
				end++
			}
			if strings.TrimSpace(string(source[start:end])) != "cicada 1" {
				return nil, false, fmt.Errorf("cannot migrate nonstandalone legacy header")
			}
			if end < len(source) {
				end++
			}
			// The blank line after a legacy header was its section separator.
			if end < len(source) && source[end] == '\n' {
				end++
			} else if end+1 < len(source) && source[end] == '\r' && source[end+1] == '\n' {
				end += 2
			}
			edits = append(edits, sourceEdit{start: start, end: end})
		case "instrument_decl":
			var explicit bool
			for j := 0; j < node.NamedChildCount(); j++ {
				if walker.Type(node.NamedChild(j)) == "instrument_octave" {
					explicit = true
					break
				}
			}
			if explicit {
				continue
			}
			span := source[node.StartByte():node.EndByte()]
			brace := bytes.IndexByte(span, '{')
			if brace < 0 {
				return nil, false, fmt.Errorf("instrument has no opening brace")
			}
			offset := int(node.StartByte()) + brace + 1
			insert := " octave = 2"
			if bytes.IndexByte(span[brace+1:], '\n') >= 0 {
				newline := "\n"
				if bytes.Contains(source, []byte("\r\n")) {
					newline = "\r\n"
				}
				insert = newline + "  octave = 2"
			}
			edits = append(edits, sourceEdit{start: offset, end: offset, text: insert})
		}
	}
	if len(edits) == 0 {
		return bytes.Clone(source), false, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	fixed := bytes.Clone(source)
	for _, edit := range edits {
		fixed = append(append(bytes.Clone(fixed[:edit.start]), edit.text...), fixed[edit.end:]...)
	}
	after, diagnostics := notation.Parse(fixed)
	if hasDiagnosticErrors(diagnostics) {
		return nil, false, fmt.Errorf("fix generated invalid score: %+v", diagnostics)
	}
	projectAfter, diagnostics := project.FromScore(after)
	if projectAfter == nil || hasDiagnosticErrors(diagnostics) || !reflect.DeepEqual(projectBefore, projectAfter) {
		return nil, false, fmt.Errorf("fix changed musical meaning")
	}
	return fixed, true, nil
}
