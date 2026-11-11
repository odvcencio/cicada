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

	edits "m31labs.dev/cicada/edit"
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
	// Matched marks a copy closed because it held the file's text: the file's
	// revision then. Such a record keeps the copy's text, and only such a
	// record, not rewritten since, may reopen.
	Matched string `json:"matched,omitempty"`
}

func sourceFingerprint(source string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(source))) }
func sourcePath(owner, id string) string     { return "sources/" + noteOwnerPrefix(owner) + id + ".json" }

// sameSourceText reports whether a recovery copy holds exactly the text of a
// file. CRLF counts as LF, because a browser cannot keep it apart: a textarea
// reports CRLF as LF, and a form sends LF as CRLF. Every other difference is
// a change: a lone CR (the grammar keeps one inside strings and comments) and
// a missing final newline included.
func sameSourceText(copy, file string) bool {
	return copy == file || strings.ReplaceAll(copy, "\r\n", "\n") == strings.ReplaceAll(file, "\r\n", "\n")
}

// fileRevision names a file's text without its bytes: revision is
// edits.Revision of the bytes, and text that of the text with each CRLF read
// as LF (a lone CR kept), which a service before text revisions does not
// name.
type fileRevision struct{ revision, text string }

func revisionOf(file string) fileRevision {
	return fileRevision{edits.Revision([]byte(file)), edits.Revision([]byte(strings.ReplaceAll(file, "\r\n", "\n")))}
}

// holds is sameSourceText for a file known by its revisions.
func (f fileRevision) holds(copy string) bool {
	lf := edits.Revision([]byte(strings.ReplaceAll(copy, "\r\n", "\n")))
	if f.text != "" {
		return lf == f.text
	}
	// Only the bytes' revision: the copy's LF or CRLF form must have it, so a
	// file that mixes the two keeps its copies.
	return f.revision != "" && (lf == f.revision || edits.Revision([]byte(strings.ReplaceAll(strings.ReplaceAll(copy, "\r\n", "\n"), "\n", "\r\n"))) == f.revision)
}

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
		if err == nil && previous.State == "closed" {
			// Save, Discard and a clean editor close a copy for good. A copy
			// closed because it held the file's text (closeSourceCopy) keeps
			// its sequence: its window may type again, and that text is kept.
			if previous.File != saved.File || previous.Sequence >= saved.Sequence {
				return errSourceCheckpoint
			}
		} else if err == nil {
			if previous.File != saved.File || previous.Revision != saved.Revision || previous.Sequence > saved.Sequence {
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

// A closed copy refuses a delayed checkpoint, one whose sequence is at or
// below the closed record's. sequence is the last edit the request (Save or
// Discard) knew of: the copy is closed at that sequence, so an edit typed
// after the request, whenever its checkpoint lands, is kept again; a copy
// that already holds newer edits is left as it is. final closes the copy for
// every later sequence, for an editor that has returned to the file's text
// and starts a new copy when it is edited again. A fork retires its parent
// only if that parent has not been edited, at the parent's own sequence.
func (s *noteStore) closeSource(owner, id, file string, sequence uint64, final bool, parentID, parentHash string) error {
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
		closed := savedSource{Version: 1, ID: id, File: file, Sequence: sequence, State: "closed", Updated: time.Now().UTC()}
		if final {
			closed.Sequence = ^uint64(0)
		}
		// A copy closed for matching the file at this very sequence is
		// rewritten too, so that a reopen in flight cannot undo this close.
		if err != nil || current.State == "ready" && current.Sequence <= sequence || current.State == "closed" && (current.Sequence < closed.Sequence || current.Sequence == closed.Sequence && current.Matched != "") {
			if err := writeSource(root, owner, closed); err != nil {
				return err
			}
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
				return writeSource(root, owner, savedSource{Version: 1, ID: parentID, File: file, Revision: parent.Revision, Sequence: parent.Sequence, State: "closed", Updated: time.Now().UTC()})
			}
		}
		return nil
	})
}

// closeSourceCopy closes a ready copy whose text the caller found equal to
// the file (whose revision was matched), under the store lock, only if it
// is still the copy that was listed: the same file, sequence and text. A
// copy edited since is left as it is. The closed copy keeps its sequence, so
// a later edit in its window is kept again (see saveSource).
func (s *noteStore) closeSourceCopy(owner string, listed savedSource, matched string) (bool, error) {
	if owner == "" || !validNoteID(listed.ID) || matched == "" {
		return false, fmt.Errorf("invalid Code recovery receipt")
	}
	closed := false
	err := s.locked(func(root *os.Root) error {
		current, err := loadSource(root, owner, listed.ID)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.State != "ready" || current.File != listed.File || current.Sequence != listed.Sequence || current.Source != listed.Source {
			return nil
		}
		closed = true
		// The text stays with the record, for a repeat of the checkpoint
		// that closed it; a Save or Discard rewrites the record without it.
		return writeSource(root, owner, savedSource{Version: 1, ID: current.ID, File: current.File, Revision: current.Revision, Sequence: current.Sequence, State: "closed", Updated: time.Now().UTC(), Matched: matched, Source: current.Source})
	})
	return closed && err == nil, err
}

// reopenSourceCopy undoes closeSourceCopy when the file turned out not to
// hold the copy's text: the copy is ready again, unless something rewrote
// the record since (a Save, a Discard, or its window writing again). kept
// reports whether a ready copy of this id is there afterwards.
func (s *noteStore) reopenSourceCopy(owner string, copy savedSource) (kept bool, err error) {
	if owner == "" || !validNoteID(copy.ID) {
		return false, fmt.Errorf("invalid Code recovery receipt")
	}
	err = s.locked(func(root *os.Root) error {
		current, err := loadSource(root, owner, copy.ID)
		if err != nil {
			return err
		}
		if current.State == "ready" {
			kept = current.Sequence >= copy.Sequence
			return nil
		}
		if current.Sequence != copy.Sequence || current.Matched == "" || current.File != copy.File || current.Source != copy.Source {
			return nil
		}
		copy.Version, copy.State, copy.Matched, kept = 1, "ready", "", true
		return writeSource(root, owner, copy)
	})
	return kept, err
}
