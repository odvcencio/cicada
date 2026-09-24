package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func projectCommand(args []string) {
	switch args[0] {
	case "convert":
		if len(args) != 4 || args[2] != "-o" || args[1] == args[3] {
			usage()
		}
		input, output := args[1], args[3]
		if filepath.Ext(input) == ".json" {
			if filepath.Ext(output) != ".cicada" {
				usage()
			}
		} else if filepath.Ext(input) == ".cicada" {
			if filepath.Ext(output) != ".json" {
				usage()
			}
		} else {
			usage()
		}
		p, err := loadProject(input)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var data []byte
		if filepath.Ext(input) == ".json" {
			data, err = project.ToSource(p)
		} else {
			data, err = project.CanonicalJSON(p)
		}
		if err == nil {
			err = writeNewAtomic(output, data)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(output)
	case "compare":
		if len(args) != 4 || args[1] != "--semantic" {
			usage()
		}
		a, err := loadProject(args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, err := loadProject(args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !project.SemanticEqual(a, b) {
			fmt.Fprintln(os.Stderr, "different")
			os.Exit(1)
		}
		fmt.Println("equal")
	default:
		usage()
	}
}

func loadProject(path string) (*project.Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s:1:1: error CICADA-IO: %w", path, err)
	}
	if strings.HasSuffix(path, ".json") {
		p, err := project.DecodeJSON(data)
		if err != nil {
			return nil, fmt.Errorf("%s:1:1: error CICADA-PARAM: %w", path, err)
		}
		return p, nil
	}
	if !strings.HasSuffix(path, ".cicada") {
		return nil, fmt.Errorf("%s:1:1: error CICADA-IO: expected .cicada or .json", path)
	}
	score, diagnostics := notation.Parse(data)
	hasError := false
	for _, d := range diagnostics {
		hasError = hasError || d.Severity == "error"
	}
	if !hasError {
		var compiled []notation.Diagnostic
		p, extra := project.FromScore(score)
		compiled = extra
		diagnostics = append(diagnostics, compiled...)
		if p != nil {
			return p, nil
		}
	}
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Position.Line != diagnostics[j].Position.Line {
			return diagnostics[i].Position.Line < diagnostics[j].Position.Line
		}
		if diagnostics[i].Position.Column != diagnostics[j].Position.Column {
			return diagnostics[i].Position.Column < diagnostics[j].Position.Column
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
	for _, d := range diagnostics {
		if d.Severity != "error" {
			continue
		}
		line, column := d.Position.Line, d.Position.Column
		if line < 1 {
			line = 1
		}
		if column < 1 {
			column = 1
		}
		return nil, fmt.Errorf("%s:%d:%d: %s %s: %s", path, line, column, d.Severity, d.Code, d.Message)
	}
	return nil, fmt.Errorf("%s:1:1: error CICADA-PARAM: project cannot compile", path)
}

func writeNewAtomic(path string, data []byte) error {
	mode := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cicada-convert-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
