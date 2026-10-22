package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/project"
)

func takeSelectionEnvelope(t takejournal.Take, revision string) edits.Envelope {
	return edits.Envelope{Version: edits.EnvelopeVersion, Revision: revision, Intents: []edits.Intent{&edits.SelectTake{ID: t.ID, Track: t.Track, Scene: t.Scene, Rate: t.Rate, Channels: t.Channels, StartFrame: t.StartFrame(), Asset: edits.TakeAsset{Name: t.Asset.Name, Path: t.Asset.Path, SHA256: t.Asset.SHA256, Frames: t.Asset.Frames, RateHz: t.Asset.RateHz, Channels: t.Asset.Channels}}}}
}

func checkStudioSourceRevision(path, expected string) error {
	if expected == "" {
		return nil
	}
	current, err := project.ReadScoreRevision(path)
	if err != nil || current != expected {
		return fmt.Errorf("%w: saved score files or libraries changed; refresh Studio before editing", edits.ErrProjectChanged)
	}
	return nil
}

func (s *studio) prepareTakeFiles(files []edits.File) ([]takejournal.SourceChange, error) {
	var changes []takejournal.SourceChange
	for _, file := range files {
		if bytes.Equal(file.Before, file.After) {
			continue
		}
		name, err := filepath.Rel(takeRoot(s.path), file.Path)
		if err != nil || !filepath.IsLocal(name) || file.Path == s.path {
			return nil, fmt.Errorf("take source is outside the project")
		}
		changes = append(changes, takejournal.SourceChange{Path: filepath.ToSlash(name), Before: file.Before, After: file.After})
	}
	return changes, nil
}

// Verify every source before resuming any write. Already applied files are
// omitted; a different external edit leaves the whole pending take conflicted.
func (s *studio) pendingTakeFiles(t takejournal.Take) ([]edits.File, error) {
	if len(t.Files) == 0 {
		return nil, nil
	}
	sources, err := project.ReadSources(s.path, nil)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]bool)
	for _, file := range sources.Files {
		owned[file.Path] = file.Path != s.path
	}
	var files []edits.File
	seen := make(map[string]bool)
	for _, file := range t.Files {
		if !filepath.IsLocal(file.Path) || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path || strings.Contains(file.Path, "\\") || seen[file.Path] {
			return nil, fmt.Errorf("invalid prepared take source")
		}
		seen[file.Path] = true
		path := filepath.Join(sources.Root, filepath.FromSlash(file.Path))
		if !owned[path] {
			return nil, fmt.Errorf("take source is no longer in the project")
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(current, file.After) {
			continue
		}
		if !bytes.Equal(current, file.Before) {
			return nil, errTakeRevision
		}
		files = append(files, edits.File{Path: path, Before: file.Before, After: file.After})
	}
	return files, nil
}

// A crash can publish the entry's comp declarations before its placement.
// If that placement was then edited elsewhere, restore only our own entry
// write, and only when its verified predecessor fits the current project.
// The external source and the journal's prepared candidate remain untouched.
func (s *studio) restoreConflictedTakeEntry(t takejournal.Take, current []byte) error {
	if len(t.Files) == 0 || t.Before == t.After || studioRevision(current) != t.After {
		return nil
	}
	before, found, err := studioRecoverySource(s.path, t.Before)
	if err != nil || !found {
		return err
	}
	if _, err := compileStudioSource(s.path, before); err != nil {
		return nil
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	_, _, err = studioWriteIfRevision(s.path, before, info.Mode().Perm(), t.After, nil)
	return err
}
