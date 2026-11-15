package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/project"
)

// songSchema names the format of cicada song --json.
const songSchema = "cicada.song.v1"

const songUsage = "usage: cicada song [--json] [--include source,symbols,structure] [score.cicada]"

// songDocument is cicada song --json. Without a score it lists the project's
// scores, each with its revision. With one, it gives the score's revision and
// what --include asks for: its source text (the default), the names it
// defines (symbols), and its compiled meaning as semantic JSON (structure).
type songDocument struct {
	Schema    string           `json:"schema"`
	Scores    []songScoreJSON  `json:"scores,omitempty"`
	File      string           `json:"file,omitempty"`
	Revision  string           `json:"revision,omitempty"`
	Source    *string          `json:"source,omitempty"`
	Symbols   []songSymbolJSON `json:"symbols,omitempty"`
	Structure json.RawMessage  `json:"structure,omitempty"`
	Error     *jsonError       `json:"error,omitempty"`
}

type songScoreJSON struct {
	File     string `json:"file"`
	Revision string `json:"revision"`
}

// songSymbolJSON is one name a score defines, at its line and column (from
// 1).
type songSymbolJSON struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// songOptions are what cicada song reads.
type songOptions struct {
	score   string
	include []string
}

var songIncludes = []string{"source", "symbols", "structure"}

// songCommand prints a score or the project's scores: as text, or with
// --json as one cicada.song.v1 document.
func songCommand(args []string, stdout io.Writer) error {
	asJSON := hasJSONFlag(args)
	options := songOptions{}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--json" || arg == "--json=true":
		case arg == "--include" && i+1 < len(args):
			i++
			options.include = strings.Split(args[i], ",")
		case !strings.HasPrefix(arg, "-") && options.score == "":
			options.score = arg
		default:
			return songFailure(stdout, asJSON, "usage", errors.New(songUsage))
		}
	}
	document, code, err := songJSON(options)
	if err != nil {
		return songFailure(stdout, asJSON, code, err)
	}
	if asJSON {
		return writeAgentJSON(stdout, document)
	}
	if document.Scores != nil {
		for _, score := range document.Scores {
			fmt.Fprintf(stdout, "%s\t%s\n", score.Revision, score.File)
		}
		return nil
	}
	fmt.Fprintf(stdout, "%s\t%s\n", document.Revision, document.File)
	if document.Source != nil {
		_, err = io.WriteString(stdout, *document.Source)
	}
	return err
}

func songFailure(stdout io.Writer, asJSON bool, code string, err error) error {
	if asJSON {
		if writeErr := writeAgentJSON(stdout, songDocument{Schema: songSchema, Error: &jsonError{Code: code, Message: err.Error()}}); writeErr != nil {
			return writeErr
		}
	}
	return err
}

// songJSON builds the song document, or returns the error code and error of
// a refusal: usage, unreadable_score, invalid_score (for structure) or
// project_unreadable.
func songJSON(options songOptions) (songDocument, string, error) {
	document := songDocument{Schema: songSchema}
	include := map[string]bool{}
	for _, name := range options.include {
		if !slices.Contains(songIncludes, name) {
			return document, "usage", fmt.Errorf("unknown --include %q; one of %s", name, strings.Join(songIncludes, ", "))
		}
		include[name] = true
	}
	if len(include) == 0 {
		include["source"] = true
	}
	if options.score == "" {
		if len(options.include) > 0 {
			return document, "usage", errors.New("--include needs a score")
		}
		root, err := projectRoot()
		if err != nil {
			return document, "project_unreadable", err
		}
		paths, err := projectScorePaths(root)
		if err != nil {
			return document, "project_unreadable", err
		}
		document.Scores = []songScoreJSON{}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				return document, "unreadable_score", err
			}
			document.Scores = append(document.Scores, songScoreJSON{File: jsonPath(path), Revision: edits.Revision(data)})
		}
		sort.Slice(document.Scores, func(i, j int) bool { return document.Scores[i].File < document.Scores[j].File })
		return document, "", nil
	}
	data, err := os.ReadFile(options.score)
	if err != nil {
		return document, "unreadable_score", err
	}
	document.File, document.Revision = jsonPath(options.score), edits.Revision(data)
	if include["source"] {
		text := string(data)
		document.Source = &text
	}
	if include["symbols"] {
		symbols, err := language.Symbols(data)
		if err != nil {
			return document, "invalid_score", err
		}
		document.Symbols = []songSymbolJSON{}
		for _, symbol := range symbols {
			if symbol.Role == "definition" {
				document.Symbols = append(document.Symbols, songSymbolJSON{Kind: symbol.Kind, Name: symbol.Name, Line: symbol.Position.Line, Column: symbol.Position.Column})
			}
		}
	}
	if include["structure"] {
		semantic, err := loadProject(options.score)
		if err != nil {
			return document, "invalid_score", err
		}
		structure, err := project.CanonicalJSON(semantic)
		if err != nil {
			return document, "invalid_score", err
		}
		document.Structure = structure
	}
	return document, "", nil
}

// librarySchema names the format of cicada lib list --json and cicada lib
// show --json.
const librarySchema = "cicada.library.v1"

// libraryDocument lists the libraries a project can import (lib list), or
// shows one (lib show).
type libraryDocument struct {
	Schema    string            `json:"schema"`
	Libraries []libraryLocation `json:"libraries,omitempty"`
	Library   *libraryShown     `json:"library,omitempty"`
	Error     *jsonError        `json:"error,omitempty"`
}

// libraryLocation is one library and where it resolves: std (embedded),
// project (the project's lib folder) or user (the user library).
type libraryLocation struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// libraryShown is one library: where it resolved, its content hash, its
// manifest, the declarations it offers and its audio files.
type libraryShown struct {
	Path         string   `json:"path"`
	Kind         string   `json:"kind"`
	Root         string   `json:"root"`
	SHA256       string   `json:"sha256"`
	Manifest     string   `json:"manifest"`
	Declarations []string `json:"declarations"`
	Assets       []string `json:"assets"`
}

// libraryJSON builds the library document: every library when path is
// empty, else the one at path. On failure it returns the error code
// (project_unreadable or unknown_library) and the error.
func libraryJSON(path string) (libraryDocument, string, error) {
	document := libraryDocument{Schema: librarySchema}
	root, err := projectRoot()
	if err != nil {
		return document, "project_unreadable", err
	}
	if path == "" {
		libraries, err := project.ListLibraries(root)
		if err != nil {
			return document, "project_unreadable", err
		}
		document.Libraries = []libraryLocation{}
		for _, library := range libraries {
			document.Libraries = append(document.Libraries, libraryLocation{Path: library.Path, Kind: library.Kind})
		}
		return document, "", nil
	}
	lib, err := project.InspectLibrary(root, path)
	if err != nil {
		return document, "unknown_library", err
	}
	shown := &libraryShown{Path: lib.Path, Kind: lib.Kind, Root: jsonPath(lib.Root), SHA256: lib.SHA256, Manifest: string(lib.ManifestSource), Declarations: libraryDeclarations(lib), Assets: append([]string{}, lib.Assets...)}
	document.Library = shown
	return document, "", nil
}
