package main

import (
	"fmt"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/edition"
)

func scoreEdition(scorePath string) (int, string, error) {
	return edition.ScoreEdition(scorePath)
}

func parseManifest(data []byte) (int, error) {
	return edition.ParseManifest(data)
}

func checkScoreEdition(path string) error {
	_, _, err := scoreEdition(path)
	return err
}

func newCommand(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: cicada new <name>")
	}
	name := args[0]
	if err := newProject(name, os.WriteFile); err != nil {
		return err
	}
	fmt.Println(filepath.Join(name, "main.cicada"))
	return nil
}

func newProject(name string, writeFile func(string, []byte, os.FileMode) error) error {
	if !edition.ValidProjectName(name) {
		return fmt.Errorf("project name must use letters, digits, hyphen, or underscore")
	}
	if err := os.Mkdir(name, 0755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(filepath.Join(name, "main.cicada"))
			_ = os.Remove(filepath.Join(name, "cicada.mod"))
			_ = os.Remove(name)
		}
	}()
	manifest := []byte("project " + name + "\ncicada 1\n")
	main := []byte("title \"" + name + "\"\ntempo 130\nkey a minor\n\ntrack bass acid {}\n\npattern pulse acid steps = 16 {\n  1 . . . 5 . . . 1 . . . 7 . . .\n}\n\nscene main { bass = pulse }\nsong { main*8 }\n")
	if err := writeFile(filepath.Join(name, "cicada.mod"), manifest, 0644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(name, "main.cicada"), main, 0644); err != nil {
		return err
	}
	complete = true
	return nil
}
