package main

import (
	"fmt"
	"path/filepath"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

// revisionRoot is the folder a project's file revisions are named from: the
// manifest's folder, or the score's when there is none.
func revisionRoot(opts edits.Options) string {
	base := opts.Path
	if opts.ManifestPath != "" {
		base = opts.ManifestPath
	}
	root, err := filepath.Abs(filepath.Dir(base))
	if err != nil {
		return filepath.Dir(base)
	}
	return root
}

// fileRevisions lists the revision of every source file of the project apart
// from libraries, each by its path relative to the project folder with
// forward slashes, as cicada apply --json and Studio report them.
func fileRevisions(opts edits.Options) []edits.FileRevision {
	root := revisionRoot(opts)
	var revisions []edits.FileRevision
	for _, file := range opts.Sources {
		if file.Library != "" {
			continue
		}
		revisions = append(revisions, edits.FileRevision{Path: projectRelative(root, file.Path), Revision: edits.Revision(file.Source)})
	}
	return revisions
}

func projectRelative(root, path string) string {
	if relative, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(relative, "..") {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(path)
}

// fileRevisionError is an envelope revision that does not hold: the file
// changed since, or it is not a source of the project.
type fileRevisionError struct {
	Path      string
	Want, Got string
	Unknown   bool
}

func (e *fileRevisionError) Error() string {
	if e.Unknown {
		return fmt.Sprintf("%s is not a source file of this project", e.Path)
	}
	return fmt.Sprintf("%s changed; intents target revision %s, file is at %s", e.Path, e.Want, e.Got)
}

// checkFileRevisions compares an envelope's revisions with the bytes the
// apply read. A path is read relative to the project folder; one that is not
// a source of the project is refused rather than read. The CLI and Studio
// both check through here.
func checkFileRevisions(opts edits.Options, want []edits.FileRevision) error {
	root := revisionRoot(opts)
	byPath := map[string]notation.SourceFile{}
	for _, file := range opts.Sources {
		if file.Library == "" {
			absolute, err := filepath.Abs(file.Path)
			if err == nil {
				byPath[absolute] = file
			}
		}
	}
	for _, revision := range want {
		path := filepath.FromSlash(revision.Path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		file, ok := byPath[filepath.Clean(path)]
		if !ok {
			return &fileRevisionError{Path: revision.Path, Unknown: true}
		}
		if got := edits.Revision(file.Source); got != revision.Revision {
			return &fileRevisionError{Path: revision.Path, Want: revision.Revision, Got: got}
		}
	}
	return nil
}
