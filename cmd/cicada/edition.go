package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/internal/agentguide"
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

// newSchema names the format of cicada new --json.
const newSchema = "cicada.new.v1"

const newUsage = "usage: cicada new [--json] <name>"

// newDocument is cicada new --json: the project folder, its score, every file
// written (sorted), and whether one of them is the project's AGENTS.md.
type newDocument struct {
	Schema          string     `json:"schema"`
	Project         string     `json:"project,omitempty"`
	Score           string     `json:"score,omitempty"`
	Files           []string   `json:"files,omitempty"`
	AgentsMDWritten bool       `json:"agents_md_written"`
	Error           *jsonError `json:"error,omitempty"`
}

func newCommand(args []string) error {
	asJSON := hasJSONFlag(args)
	var names []string
	for _, arg := range args {
		if arg != "--json" && arg != "--json=true" {
			names = append(names, arg)
		}
	}
	fail := func(code string, err error) error {
		if asJSON {
			if writeErr := writeAgentJSON(os.Stdout, newDocument{Schema: newSchema, Error: &jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	if len(names) != 1 || strings.HasPrefix(names[0], "-") {
		return fail("usage", fmt.Errorf("%s", newUsage))
	}
	name := names[0]
	files, err := newProjectFiles(name, os.WriteFile)
	if err != nil {
		switch {
		case !edition.ValidProjectName(name):
			return fail("invalid_name", err)
		case errors.Is(err, os.ErrExist):
			return fail("destination_exists", err)
		}
		return fail("write", err)
	}
	if !asJSON {
		fmt.Println(filepath.Join(name, "main.cicada"))
		return nil
	}
	document := newDocument{Schema: newSchema, Project: jsonPath(name), Score: jsonPath(filepath.Join(name, "main.cicada"))}
	for _, file := range files {
		document.Files = append(document.Files, jsonPath(file))
		document.AgentsMDWritten = document.AgentsMDWritten || filepath.Base(file) == "AGENTS.md"
	}
	sort.Strings(document.Files)
	return writeAgentJSON(os.Stdout, document)
}

// starterBody is the score `cicada new` writes after its title line: drums, an
// FM bass line and poly-keys chords, in two scenes that differ so the song has
// a break. It is already formatted and passes `cicada fix --check`, which is
// why the break stops the drums with `stop`, the canonical word, not `off`.
// examples/first-song holds the same score under a fixed title, and
// TestStarterMatchesFirstSongExample keeps the two identical.
const starterBody = `
tempo 120

key a minor

track drums drums {}

track bass fm_bass {}

track chords poly_keys {
  octave = 3
}

pattern beat drums {
  bd: X... .... X... ....
  sd: .... X... .... X...
  ch: x.x. x.x. x.x. x.x.
}

pattern pulse {
  1 . . 1 . . 1 . 6, . . 6, . . 7, .
}

pattern progression {
  [1 3 5] - - - - - - . [6, 1 3] - - - - - - .
}

scene main {
  drums = beat
  bass = pulse
  chords = progression
}

scene break {
  drums = stop
  bass = pulse
  chords = progression
}

song {
  main*4
  break*4
}
`

func newProject(name string, writeFile func(string, []byte, os.FileMode) error) error {
	_, err := newProjectFiles(name, writeFile)
	return err
}

// newProjectFiles creates the project and returns the files it wrote.
func newProjectFiles(name string, writeFile func(string, []byte, os.FileMode) error) ([]string, error) {
	if !edition.ValidProjectName(name) {
		return nil, fmt.Errorf("project name must use letters, digits, hyphen, or underscore")
	}
	target, err := filetarget.Create(name)
	if err != nil {
		return nil, err
	}
	dir := target.Physical
	if err := os.Mkdir(dir, 0755); err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(filepath.Join(dir, "main.cicada"))
			_ = os.Remove(filepath.Join(dir, "cicada.mod"))
			_ = os.Remove(filepath.Join(dir, ".gitignore"))
			_ = os.Remove(filepath.Join(dir, "AGENTS.md"))
			_ = os.Remove(dir)
		}
	}()
	manifest := []byte("project " + name + "\ncicada 2\n")
	main := []byte("title \"" + name + "\"\n" + starterBody)
	document, err := notation.ParseDocument(main)
	if err != nil {
		return nil, err
	}
	main, err = notation.Format(document)
	if err != nil {
		return nil, err
	}
	gitignore := []byte(".cicada-studio-*\n*.revision\n.cicada/\ncicada.local\n")
	files := []string{filepath.Join(name, "cicada.mod"), filepath.Join(name, "main.cicada"), filepath.Join(name, ".gitignore")}
	for i, data := range [][]byte{manifest, main, gitignore} {
		if err := writeFile(filepath.Join(dir, filepath.Base(files[i])), data, 0644); err != nil {
			return nil, err
		}
	}
	// An agent that opens the project reads its AGENTS.md first: a short
	// pointer to the quick start of the installed binary. One already there
	// is never replaced.
	if _, err := os.Lstat(filepath.Join(dir, "AGENTS.md")); errors.Is(err, os.ErrNotExist) {
		if err := writeFile(filepath.Join(dir, "AGENTS.md"), []byte(agentguide.ProjectAgents()), 0644); err != nil {
			return nil, err
		}
		files = append(files, filepath.Join(name, "AGENTS.md"))
	}
	complete = true
	return files, nil
}
