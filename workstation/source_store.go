package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var errSourceCheckpoint = errors.New("a newer or finished recovery copy already exists")

type savedSource struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	Sequence   uint64    `json:"sequence"`
	File       string    `json:"file"`
	Revision   string    `json:"revision"`
	Source     string    `json:"source"`
	State      string    `json:"state"`
	ParentID   string    `json:"parent_id,omitempty"`
	ParentHash string    `json:"parent_hash,omitempty"`
	Updated    time.Time `json:"updated"`
}

func sourceFingerprint(source string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(source))) }
func sourcePath(owner, id string) string     { return "sources/" + noteOwnerPrefix(owner) + id + ".json" }

func validSourceFile(file string) bool {
	return len(file) <= 1024 && (file == "" || filepath.IsLocal(file) && !strings.Contains(file, "\\") && filepath.ToSlash(filepath.Clean(file)) == file)
}

func loadSource(root *os.Root, owner, id string) (savedSource, error) {
	var saved savedSource
	if owner == "" || !validNoteID(id) {
		return saved, fs.ErrNotExist
	}
	data, err := readNoteFile(root, sourcePath(owner, id), noteFileLimit)
	if err != nil {
		return saved, err
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return saved, fmt.Errorf("Code recovery copy is unreadable; its file is retained: %w", err)
	}
	if saved.Version != 1 || saved.ID != id || saved.Sequence == 0 || !validSourceFile(saved.File) || saved.State != "ready" && saved.State != "closed" || len(saved.Source) > 2<<20 || saved.State == "ready" && saved.Revision == "" {
		return savedSource{}, fmt.Errorf("Code recovery copy is invalid; its file is retained")
	}
	return saved, nil
}

func writeSource(root *os.Root, owner string, saved savedSource) error {
	if err := root.MkdirAll("sources", 0700); err != nil {
		return err
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return writeNoteFile(root, sourcePath(owner, saved.ID), data)
}

func (s *noteStore) saveSource(owner string, saved savedSource) (savedSource, error) {
	if owner == "" || !validNoteID(saved.ID) || saved.Sequence == 0 || !validSourceFile(saved.File) || saved.Revision == "" || len(saved.Revision) > 128 || len(saved.Source) > 2<<20 || len(saved.ParentHash) > 128 || saved.ParentID != "" && !validNoteID(saved.ParentID) {
		return savedSource{}, fmt.Errorf("invalid Code recovery copy")
	}
	saved.Version, saved.State, saved.Updated = 1, "ready", time.Now().UTC()
	err := s.locked(func(root *os.Root) error {
		previous, err := loadSource(root, owner, saved.ID)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil {
			if previous.State == "closed" || previous.File != saved.File || previous.Revision != saved.Revision || previous.Sequence > saved.Sequence {
				return errSourceCheckpoint
			}
			if previous.Sequence == saved.Sequence {
				if previous.Source != saved.Source {
					return errSourceCheckpoint
				}
				saved = previous
				return nil
			}
		}
		return writeSource(root, owner, saved)
	})
	return saved, err
}

func (s *noteStore) sourceCopies(owner, file string) ([]savedSource, error) {
	var copies []savedSource
	if owner == "" {
		return copies, nil
	}
	err := s.locked(func(root *os.Root) error {
		dir, err := root.Open("sources")
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return err
		}
		prefix := noteOwnerPrefix(owner)
		var unreadable error
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
			copy, err := loadSource(root, owner, id)
			if err != nil {
				unreadable = errors.Join(unreadable, err)
				continue
			}
			if copy.File == file && copy.State == "ready" {
				copies = append(copies, copy)
			}
		}
		return unreadable
	})
	sort.Slice(copies, func(i, j int) bool { return copies[i].Updated.After(copies[j].Updated) })
	return copies, err
}

func (s *noteStore) getSource(owner, id string) (savedSource, error) {
	var saved savedSource
	err := s.locked(func(root *os.Root) error {
		var err error
		saved, err = loadSource(root, owner, id)
		return err
	})
	return saved, err
}

// Tombstones also reject a delayed checkpoint that arrives after Save or
// Discard. A fork retires its parent only if that parent has not been edited.
func (s *noteStore) closeSource(owner, id, file, parentID, parentHash string) error {
	if owner == "" || !validNoteID(id) || !validSourceFile(file) {
		return fmt.Errorf("invalid Code recovery receipt")
	}
	return s.locked(func(root *os.Root) error {
		current, err := loadSource(root, owner, id)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil && current.File != file {
			return errSourceCheckpoint
		}
		closed := savedSource{Version: 1, ID: id, File: file, Sequence: ^uint64(0), State: "closed", Updated: time.Now().UTC()}
		if err := writeSource(root, owner, closed); err != nil {
			return err
		}
		if validNoteID(parentID) && parentID != id {
			parent, err := loadSource(root, owner, parentID)
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if parent.File == file && parent.State == "ready" && sourceFingerprint(parent.Source) == parentHash {
				closed.ID = parentID
				return writeSource(root, owner, closed)
			}
		}
		return nil
	})
}
