package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/notation"
)

// SourceError reports manifest and source loading failures with a stable code
// and source location. Unwrap retains the underlying filesystem error.
type SourceError struct {
	Diagnostic notation.Diagnostic
	Cause      error
}

func (e *SourceError) Error() string {
	return e.Diagnostic.Error()
}
func (e *SourceError) Unwrap() error { return e.Cause }

// Sources holds exact file bytes in load order, including editor overrides.
type Sources struct {
	Root         string
	ManifestPath string
	Manifest     edition.Manifest
	Files        []notation.SourceFile
	Libraries    map[string]*Library
	LibraryOrder []string
	Imports      []notation.Import
	Bindings     map[string]map[string]string
}

// ReadSources loads the closest explicit manifest, or the requested loose or
// legacy single-file score. Overrides are indexed by absolute file path.
func ReadSources(path string, overrides map[string][]byte) (*Sources, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	_, manifestPath, err := edition.ScoreEdition(absolute)
	if err != nil {
		var manifestError *edition.ManifestError
		if errors.As(err, &manifestError) {
			return nil, sourceError(manifestPath, manifestError.Line, manifestError.Column, manifestError.Code, manifestError.Message, err)
		}
		return nil, err
	}
	set := &Sources{Root: filepath.Dir(absolute), ManifestPath: manifestPath}
	paths := []string{absolute}
	if manifestPath != "" {
		set.Root = filepath.Dir(manifestPath)
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		set.Manifest, err = edition.ParseProjectManifest(data)
		if err != nil {
			return nil, err
		}
		if set.Manifest.Library != "" {
			// A vendored library source belongs to the containing score for
			// tooling. Load that score only if it actually imports this file.
			dir := filepath.Dir(set.Root)
			for {
				data, readErr := os.ReadFile(filepath.Join(dir, "cicada.mod"))
				if readErr == nil {
					parent, parseErr := edition.ParseProjectManifest(data)
					if parseErr != nil {
						return nil, parseErr
					}
					if parent.Project != "" && parent.Entry != "" {
						containing, loadErr := ReadSources(filepath.Join(dir, filepath.FromSlash(parent.Entry)), overrides)
						if loadErr != nil {
							return nil, loadErr
						}
						for _, file := range containing.Files {
							if file.Path == absolute {
								return containing, nil
							}
						}
						return nil, sourceError(path, 1, 1, "CICADA-SOURCE-PATH", "library source is not imported by the project", nil)
					}
				}
				next := filepath.Dir(dir)
				if next == dir {
					break
				}
				dir = next
			}
		}
		if set.Manifest.ExplicitSources() {
			paths = nil
			listed := false
			for _, name := range set.Manifest.SourcePaths() {
				full := filepath.Join(set.Root, filepath.FromSlash(name))
				paths = append(paths, full)
				listed = listed || full == absolute
			}
			if !listed {
				return nil, sourceError(path, 1, 1, "CICADA-SOURCE-PATH", "score is not listed in cicada.mod", nil)
			}
		}
	}
	if !set.Manifest.ExplicitSources() {
		data, ok := overrides[absolute]
		if !ok {
			data, err = os.ReadFile(absolute)
			if err != nil {
				return nil, sourceError(path, 1, 1, "CICADA-SOURCE-MISSING", "cannot read score: "+err.Error(), err)
			}
		}
		set.Files = []notation.SourceFile{{Path: path, Source: data}}
		if err := set.readLibraries(overrides); err != nil {
			return nil, err
		}
		return set, nil
	}
	root, err := os.OpenRoot(set.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, full := range paths {
		name, err := filepath.Rel(set.Root, full)
		if err != nil {
			return nil, err
		}
		// Resolve through os.Root even for open editor buffers. An override must
		// not turn an escaping symlink into a valid project source.
		data, readErr := root.ReadFile(name)
		if readErr != nil {
			code := "CICADA-SOURCE-MISSING"
			if !errors.Is(readErr, os.ErrNotExist) && !errors.Is(readErr, os.ErrPermission) {
				code = "CICADA-SOURCE-PATH"
			}
			where, line := full, 1
			if set.Manifest.ExplicitSources() {
				where, line = manifestPath, set.Manifest.Lines[filepath.ToSlash(name)]
			}
			return nil, sourceError(where, line, 1, code, fmt.Sprintf("cannot load source %q: %v", filepath.ToSlash(name), readErr), readErr)
		}
		if replacement, ok := overrides[full]; ok {
			data = replacement
		}
		set.Files = append(set.Files, notation.SourceFile{Path: full, Source: data})
	}
	if err := set.readLibraries(overrides); err != nil {
		return nil, err
	}
	return set, nil
}

func sourceError(file string, line, col int, code, message string, cause error) *SourceError {
	return &SourceError{Diagnostic: notation.Diagnostic{Code: code, Severity: "error", Message: message, Position: notation.Position{File: file, Line: line, Column: col}}, Cause: cause}
}

// Parse resolves all project declarations before validating references and
// assets. A manifest without entry/source keeps the legacy parsing behavior.
func (s *Sources) Parse() (*notation.Score, []notation.Diagnostic) {
	var score *notation.Score
	var diagnostics []notation.Diagnostic
	if s.ManifestPath == "" && len(s.Libraries) == 0 {
		score, diagnostics = notation.ParseSource(s.Files[0])
	} else {
		score, diagnostics = notation.ParseFiles(s.Files, s.sourceEdition())
	}
	diagnostics = append(diagnostics, s.verifyLibraries()...)
	diagnostics = append(diagnostics, VerifyAssets(score, s.Root)...)
	if score != nil {
		for i := range score.Assets {
			if score.Assets[i].Root == "" {
				score.Assets[i].Root = s.Root
			}
		}
	}
	return score, diagnostics
}

// LoadScore is shared by commands and editors. Compilation remains a separate
// stage so tools can inspect the positioned source model.
func LoadScore(path string, overrides map[string][]byte) (*notation.Score, []notation.Diagnostic, error) {
	sources, err := ReadSources(path, overrides)
	if err != nil {
		return nil, nil, err
	}
	score, diagnostics := sources.Parse()
	return score, diagnostics, nil
}

func (s *Sources) sourceEdition() int {
	if s.Manifest.Edition != 0 {
		return s.Manifest.Edition
	}
	return notation.SourceEdition(s.Files[0])
}
