package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// scoreEdition finds the closest manifest. A loose score and a legacy header
// both use edition 1 until a later edition has a migration path.
func scoreEdition(scorePath string) (int, string, error) {
	dir, err := filepath.Abs(filepath.Dir(scorePath))
	if err != nil {
		return 0, "", err
	}
	for {
		path := filepath.Join(dir, "cicada.mod")
		data, err := os.ReadFile(path)
		if err == nil {
			edition, parseErr := parseManifest(data)
			if parseErr != nil {
				return 0, path, fmt.Errorf("%s: %w", path, parseErr)
			}
			return edition, path, nil
		}
		if !os.IsNotExist(err) {
			return 0, path, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 1, "", nil
		}
		dir = parent
	}
}

func parseManifest(data []byte) (int, error) {
	var name string
	var edition int
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		parts := strings.Fields(text)
		if len(parts) != 2 {
			return 0, fmt.Errorf("line %d: expected directive and value", line)
		}
		switch parts[0] {
		case "project":
			if name != "" || !projectName.MatchString(parts[1]) {
				return 0, fmt.Errorf("line %d: invalid or duplicate project name", line)
			}
			name = parts[1]
		case "cicada":
			if edition != 0 {
				return 0, fmt.Errorf("line %d: duplicate cicada edition", line)
			}
			value, err := strconv.Atoi(parts[1])
			if err != nil || value < 1 {
				return 0, fmt.Errorf("line %d: invalid cicada edition", line)
			}
			edition = value
		default:
			return 0, fmt.Errorf("line %d: unknown directive %q", line, parts[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if name == "" || edition == 0 {
		return 0, fmt.Errorf("manifest requires project and cicada directives")
	}
	if edition != 1 {
		return 0, fmt.Errorf("CICADA-VERSION: only cicada 1 is supported")
	}
	return edition, nil
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
	if !projectName.MatchString(name) {
		return fmt.Errorf("project name must use letters, digits, hyphen, or underscore")
	}
	if err := os.Mkdir(name, 0755); err != nil {
		return err
	}
	manifest := []byte("project " + name + "\ncicada 1\n")
	main := []byte("title \"" + name + "\"\ntempo 130\nkey a minor\n\ntrack bass acid {}\n\npattern pulse acid steps = 16 {\n  1 . . . 5 . . . 1 . . . 7 . . .\n}\n\nscene main { bass = pulse }\nsong { main*8 }\n")
	if err := os.WriteFile(filepath.Join(name, "cicada.mod"), manifest, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(name, "main.cicada"), main, 0644); err != nil {
		return err
	}
	fmt.Println(filepath.Join(name, "main.cicada"))
	return nil
}
