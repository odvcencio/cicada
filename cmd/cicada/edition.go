package main

import (
	"fmt"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/internal/filetarget"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
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

func parseScoreForPath(path string, source []byte) (*notation.Score, []notation.Diagnostic, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	return project.LoadScore(path, map[string][]byte{absolute: source})
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
	dir, err := filetarget.Create(name)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(filepath.Join(dir, "main.cicada"))
			_ = os.Remove(filepath.Join(dir, "cicada.mod"))
			_ = os.Remove(filepath.Join(dir, ".gitignore"))
			_ = os.Remove(dir)
		}
	}()
	manifest := []byte("project " + name + "\ncicada 2\n")
	main := []byte("title \"" + name + "\"\ntempo 130\nkey a minor\n\ntrack bass acid {}\ntrack drums drums {}\n\npattern pulse acid {\n  1 . . . 5 . . . 1 . . . 7 . . .\n}\n\npattern beat drums {\n  bd: X... .... X... ....\n  sd: .... X... .... X...\n  ch: x.x. x.x. x.x. x.x.\n}\n\nscene main { bass = pulse drums = beat }\nscene break { bass = pulse drums = beat }\nsong { main*4 break*4 }\n")
	document, err := notation.ParseDocument(main)
	if err != nil {
		return err
	}
	main, err = notation.Format(document)
	if err != nil {
		return err
	}
	gitignore := []byte(".cicada-studio-*\n*.revision\n.cicada/\ncicada.local\n")
	if err := writeFile(filepath.Join(dir, "cicada.mod"), manifest, 0644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "main.cicada"), main, 0644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, ".gitignore"), gitignore, 0644); err != nil {
		return err
	}
	complete = true
	return nil
}
