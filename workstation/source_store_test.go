package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceCopiesSurviveRestartAndKeepOtherWriters(t *testing.T) {
	dir := t.TempDir()
	store, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	parent := savedSource{ID: newNoteID(), Sequence: 1, File: "parts/keys.cicada", Revision: "saved", Source: "// idea 🎹\n"}
	parent, err = store.saveSource("owner", parent)
	if err != nil {
		t.Fatal(err)
	}
	child := savedSource{ID: newNoteID(), Sequence: 1, File: parent.File, Revision: parent.Revision, Source: "// new variation\n", ParentID: parent.ID, ParentHash: sourceFingerprint(parent.Source)}
	if _, err := store.saveSource("owner", child); err != nil {
		t.Fatal(err)
	}
	store, _, err = openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	copies, err := store.sourceCopies("owner", parent.File)
	if err != nil || len(copies) != 2 || copies[0].ID != child.ID {
		t.Fatal("restart lost a recovery copy", copies, err)
	}
	if got, err := store.sourceCopies("another owner", parent.File); err != nil || len(got) != 0 {
		t.Fatal("another session read private Code", err)
	}
	if got, err := store.sourceCopies("owner", "main.cicada"); err != nil || len(got) != 0 {
		t.Fatal("a recovery copy crossed project files", err)
	}
	parent.Sequence++
	parent.Source = "// the other window kept editing\n"
	if _, err := store.saveSource("owner", parent); err != nil {
		t.Fatal(err)
	}
	if err := store.closeSource("owner", child.ID, child.File, child.ParentID, child.ParentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveSource("owner", child); !errors.Is(err, errSourceCheckpoint) {
		t.Fatal("a delayed checkpoint revived a finished copy", err)
	}
	copies, err = store.sourceCopies("owner", parent.File)
	if err != nil || len(copies) != 1 || copies[0].Source != parent.Source {
		t.Fatal("saving a fork discarded another writer's changes", copies, err)
	}
	info, err := os.Stat(filepath.Join(dir, sourcePath("owner", parent.ID)))
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("source recovery was not private", err)
	}
}

func TestSourceCopiesRejectOlderWritesAndRetainCorruptData(t *testing.T) {
	dir := t.TempDir()
	store, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	copy := savedSource{ID: newNoteID(), Sequence: 2, File: "main.cicada", Revision: "saved", Source: "// latest\n"}
	if _, err := store.saveSource("owner", copy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveSource("owner", copy); err != nil {
		t.Fatal("idempotent retry failed", err)
	}
	copy.Sequence--
	if _, err := store.saveSource("owner", copy); !errors.Is(err, errSourceCheckpoint) {
		t.Fatal("older writes were accepted", err)
	}
	path := filepath.Join(dir, sourcePath("owner", copy.ID))
	broken := []byte("broken recovery JSON")
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	copy.Sequence = 3
	if _, err := store.saveSource("owner", copy); err == nil {
		t.Fatal("unreadable recovery was overwritten")
	}
	if data, _ := os.ReadFile(path); string(data) != string(broken) {
		t.Fatal("corrupt recovery data was discarded")
	}
	readable := savedSource{ID: newNoteID(), Sequence: 1, File: copy.File, Revision: "saved", Source: "// another recoverable idea\n"}
	if _, err := store.saveSource("owner", readable); err != nil {
		t.Fatal(err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err == nil || len(copies) != 1 || copies[0].ID != readable.ID {
		t.Fatal("one corrupt file hid the remaining recovery copies", copies, err)
	}
	copy.ID, copy.File = newNoteID(), "../escape.cicada"
	if _, err := store.saveSource("owner", copy); err == nil {
		t.Fatal("invalid recovery file scope was accepted")
	}
}
