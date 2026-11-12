// Package benchscore holds the reference scores that measure what one edit
// costs (spec studio-local-first revision 1, section E.1):
//
//   - REF-S, the scene fixture of Studio's edit benchmark
//     (workstation/browser/edit-undo-benchmark.cjs): 567 bytes, one track, two
//     64-step patterns, two scenes and a song.
//   - REF-P, its placement fixture: examples/songs/tidal-glass.cicada under the
//     benchmark's marker line, 2,596 bytes, four tracks and nine placements.
//   - REF-L, a generated score with the size and shape of the largest
//     production (309,498 bytes; see Large).
//
// Load returns any of them with the entities its hot edits act on, so the
// native benchmark, the js/wasm benchmark and later checks read one set of
// bytes. The SHA-256 of each reference is pinned in references_test.go: a
// baseline only compares with a later run on the same bytes.
package benchscore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The names of the three references.
const (
	Small     = "REF-S"
	Placement = "REF-P"
	LargeName = "REF-L"
)

// Names lists the references in the order reports print them.
var Names = []string{Small, Placement, LargeName}

// Reference is one reference score and the entities its hot edits act on.
type Reference struct {
	Name   string
	Source []byte
	// StepPattern is a step pattern that togglestep and setpitch edit.
	StepPattern string
	// NotesPattern is the pattern whose notes an EditNotes move names: the
	// pattern with the most notes, or REF-L's 512-note performance.
	NotesPattern string
	// Placement is a placement that has room to move one beat later; "" when
	// the score has no arrange block (REF-S).
	Placement string
}

// The marker lines Studio's browser runners require before they edit a score
// (workstation/browser/continuity.test.cjs and placements.test.cjs).
const (
	sceneMarker     = "// cicada-continuity-test: disposable fixture"
	placementMarker = "// cicada-placement-test: disposable fixture"
)

// Load returns the named reference. REF-S and REF-P are read from the
// repository, so Load finds its root from the working directory (go test runs
// in the package directory) or from CICADA_REPO.
func Load(name string) (Reference, error) {
	switch name {
	case Small:
		source, err := readRepo("cmd/cicada/testdata/studio-continuity.cicada")
		if err != nil {
			return Reference{}, err
		}
		if !bytes.Contains(source, []byte(sceneMarker+"\n")) {
			return Reference{}, fmt.Errorf("benchscore: the scene fixture lost its marker line %q", sceneMarker)
		}
		return Reference{Name: name, Source: source, StepPattern: "continuity", NotesPattern: "continuity"}, nil
	case Placement:
		song, err := readRepo("examples/songs/tidal-glass.cicada")
		if err != nil {
			return Reference{}, err
		}
		// placements.test.cjs writes the marker line above the example.
		source := append([]byte(placementMarker+"\n"), song...)
		return Reference{Name: name, Source: source, StepPattern: "ripple", NotesPattern: "tide", Placement: "far-in"}, nil
	case LargeName:
		return Reference{Name: name, Source: Large(), StepPattern: LargeStepPattern, NotesPattern: LargePerformance, Placement: LargePlacement}, nil
	}
	return Reference{}, fmt.Errorf("benchscore: unknown reference %q (known: REF-S, REF-P, REF-L)", name)
}

// All returns the three references in report order.
func All() ([]Reference, error) {
	references := make([]Reference, 0, len(Names))
	for _, name := range Names {
		reference, err := Load(name)
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	return references, nil
}

// readRepo reads a file at a path relative to the repository root.
func readRepo(relative string) ([]byte, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
}

// RepoRoot returns the root of the m31labs.dev/cicada module: CICADA_REPO when
// it is set, else the nearest parent of the working directory whose go.mod
// declares that module.
func RepoRoot() (string, error) {
	if root := os.Getenv("CICADA_REPO"); root != "" {
		return root, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && bytes.HasPrefix(data, []byte("module m31labs.dev/cicada\n")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("benchscore: no m31labs.dev/cicada go.mod above the working directory; set CICADA_REPO")
		}
		dir = parent
	}
}
