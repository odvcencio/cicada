package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"m31labs.dev/cicada/workstation/control"
)

const noteFileLimit = 16 << 20

var errNoteCheckpoint = errors.New("a newer or finished copy of this performance is already saved")

type savedNotes struct {
	Version  int                 `json:"version"`
	ID       string              `json:"id"`
	Sequence uint64              `json:"sequence"`
	Revision string              `json:"revision"`
	State    string              `json:"state"`
	Updated  time.Time           `json:"updated"`
	Takes    []control.Recording `json:"takes"`
}

type noteStore struct{ dir string }

// Sessions and private note files share a workspace-specific, owner-only
// directory. A restart keeps their identity even when localhost ports change.
func openNoteStore(dir string) (*noteStore, [32]byte, error) {
	var key [32]byte
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, key, err
	}
	if err = os.MkdirAll(filepath.Join(dir, "notes"), 0700); err != nil {
		return nil, key, err
	}
	s := &noteStore{dir: dir}
	err = s.locked(func(root *os.Root) error {
		data, err := readNoteFile(root, "session.key", 32)
		if errors.Is(err, os.ErrNotExist) {
			if _, err = rand.Read(key[:]); err != nil {
				return err
			}
			return writeNoteFile(root, "session.key", key[:])
		}
		if err != nil {
			return err
		}
		if len(data) != len(key) {
			return fmt.Errorf("saved workspace session key is unreadable; existing performances were preserved")
		}
		copy(key[:], data)
		return nil
	})
	return s, key, err
}

func (s *noteStore) locked(fn func(*os.Root) error) error {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := root.OpenFile("notes.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = lockNoteStore(lock); err != nil {
		return err
	}
	defer unlockNoteStore(lock)
	return fn(root)
}

func readNoteFile(root *os.Root, path string, limit int64) ([]byte, error) {
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("saved performance exceeds its size limit")
	}
	return data, err
}

func writeNoteFile(root *os.Root, path string, data []byte) error {
	temp := path + "." + rand.Text() + ".tmp"
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = root.Rename(temp, path)
	}
	if err != nil {
		return err
	}
	d, err := root.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return syncNoteDirectory(d)
}

func validNoteID(id string) bool {
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 16 && id == strings.ToLower(id)
}

func noteOwnerPrefix(owner string) string {
	h := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(h[:]) + "-"
}

func notePath(owner, id string) string { return "notes/" + noteOwnerPrefix(owner) + id + ".json" }

func loadSavedNotes(root *os.Root, owner, id string) (savedNotes, error) {
	var saved savedNotes
	if owner == "" || !validNoteID(id) {
		return saved, os.ErrNotExist
	}
	data, err := readNoteFile(root, notePath(owner, id)+".closed", noteFileLimit)
	if errors.Is(err, os.ErrNotExist) {
		data, err = readNoteFile(root, notePath(owner, id), noteFileLimit)
	}
	if err != nil {
		return saved, err
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		return saved, fmt.Errorf("saved note performance is unreadable; its file was preserved: %w", err)
	}
	if saved.Version < 1 || saved.Version > 3 || saved.ID != id || saved.Sequence == 0 || saved.Revision == "" || len(saved.Revision) > 128 {
		return saved, fmt.Errorf("saved note performance metadata is invalid; its file was preserved")
	}
	switch saved.State {
	case "recording", "ready", "applied", "discarded":
	default:
		return saved, fmt.Errorf("saved note performance state is invalid")
	}
	if err = control.ValidateRecordings(saved.Takes); err != nil {
		return saved, err
	}
	return saved, nil
}

func (s *noteStore) get(owner, id string) (savedNotes, error) {
	var saved savedNotes
	err := s.locked(func(root *os.Root) error { var err error; saved, err = loadSavedNotes(root, owner, id); return err })
	return saved, err
}

func (s *noteStore) save(owner string, saved savedNotes) (savedNotes, error) {
	if owner == "" || !validNoteID(saved.ID) || saved.Sequence == 0 || saved.Revision == "" || len(saved.Revision) > 128 || saved.State != "recording" && saved.State != "ready" {
		return saved, fmt.Errorf("invalid note checkpoint")
	}
	if err := control.ValidateRecordings(saved.Takes); err != nil {
		return saved, err
	}
	err := s.locked(func(root *os.Root) error {
		old, err := loadSavedNotes(root, owner, saved.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			a, _ := json.Marshal(old.Takes)
			b, _ := json.Marshal(saved.Takes)
			if old.Sequence == saved.Sequence && old.State == saved.State && old.Revision == saved.Revision && bytes.Equal(a, b) {
				saved = old
				return nil
			}
			if old.State == "ready" && !bytes.Equal(a, b) {
				return errNoteCheckpoint
			}
			if saved.Sequence <= old.Sequence || old.State == "applied" || old.State == "discarded" || old.State == "ready" && saved.State == "recording" || old.Revision != saved.Revision {
				return errNoteCheckpoint
			}
		}
		saved.Version, saved.Updated = 1, time.Now().UTC()
		for _, take := range saved.Takes {
			if len(take.Groups) > 0 {
				// Older apps must refuse shared-control checkpoints rather than
				// decode the notes while silently dropping their shared layer.
				saved.Version = max(saved.Version, 2)
				for _, group := range take.Groups {
					if group.Kind != "" {
						saved.Version = 3
					}
				}
			}
		}
		data, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		if len(data) > noteFileLimit {
			return fmt.Errorf("note checkpoint exceeds its size limit")
		}
		return writeNoteFile(root, notePath(owner, saved.ID), data)
	})
	return saved, err
}

func (s *noteStore) archive(owner, id, state string) error {
	if state != "applied" && state != "discarded" {
		return fmt.Errorf("invalid performance archive state")
	}
	return s.locked(func(root *os.Root) error {
		saved, err := loadSavedNotes(root, owner, id)
		if err != nil {
			return err
		}
		saved.State, saved.Updated = state, time.Now().UTC()
		data, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		// The closed receipt wins even if a crash leaves the old checkpoint
		// beside it. Recovery scans never decode already-handled history.
		path := notePath(owner, id)
		if err = writeNoteFile(root, path+".closed", data); err != nil {
			return err
		}
		if err = root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		d, err := root.Open("notes")
		if err != nil {
			return err
		}
		defer d.Close()
		return syncNoteDirectory(d)
	})
}

func (s *noteStore) pending(owner string) ([]savedNotes, error) {
	return s.listNotes(owner, false)
}

func (s *noteStore) listNotes(owner string, includeClosed bool) ([]savedNotes, error) {
	var saved []savedNotes
	err := s.locked(func(root *os.Root) error {
		d, err := root.Open("notes")
		if err != nil {
			return err
		}
		defer d.Close()
		names, err := d.Readdirnames(-1)
		if err != nil {
			return err
		}
		prefix := noteOwnerPrefix(owner)
		seen := map[string]bool{}
		for _, name := range names {
			closed := strings.HasSuffix(name, ".json.closed")
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") && (!includeClosed || !closed) {
				continue
			}
			id := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".closed"), ".json")
			if seen[id] {
				continue
			}
			seen[id] = true
			take, err := loadSavedNotes(root, owner, id)
			if err != nil {
				return err
			}
			if includeClosed || take.State == "recording" || take.State == "ready" {
				saved = append(saved, take)
			}
		}
		return nil
	})
	sort.Slice(saved, func(i, j int) bool { return saved[i].Updated.After(saved[j].Updated) })
	return saved, err
}
