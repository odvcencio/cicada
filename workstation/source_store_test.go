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
	if err := store.closeSource("owner", child.ID, child.File, child.Sequence, false, child.ParentID, child.ParentHash); err != nil {
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

// A copy closed because it held the file's text is closed only if it is still
// the copy that was listed, and a newer edit in its window is kept again.
func TestACopyClosedForMatchingTheFileIsGuardedAndReopens(t *testing.T) {
	store, _, err := openNoteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copy := savedSource{ID: newNoteID(), Sequence: 1, File: "main.cicada", Revision: "saved", Source: "// equal to the file\n"}
	listed, err := store.saveSource("owner", copy)
	if err != nil {
		t.Fatal(err)
	}
	// The window checkpoints between the listing and the close.
	edited := copy
	edited.Sequence, edited.Source = 2, "// typed after the listing\n"
	if _, err := store.saveSource("owner", edited); err != nil {
		t.Fatal(err)
	}
	if closed, err := store.closeSourceCopy("owner", listed); closed || err != nil {
		t.Fatal("a copy edited after it was listed was closed", closed, err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err != nil || len(copies) != 1 || copies[0].Source != edited.Source {
		t.Fatal("the edited copy was not kept", copies, err)
	}
	current, err := store.getSource("owner", copy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed, err := store.closeSourceCopy("owner", current); !closed || err != nil {
		t.Fatal("an unchanged copy was not closed", closed, err)
	}
	if _, err := store.saveSource("owner", edited); !errors.Is(err, errSourceCheckpoint) {
		t.Fatal("a repeated checkpoint reopened a closed copy", err)
	}
	edited.Sequence, edited.Source = 3, "// the window types again\n"
	if _, err := store.saveSource("owner", edited); err != nil {
		t.Fatal("a newer edit was refused", err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err != nil || len(copies) != 1 || copies[0].Source != edited.Source {
		t.Fatal("the newer edit was not kept", copies, err)
	}
}

// Save and Discard close a copy up to the last edit they knew of: an edit
// already kept is left as it is, and a later one opens the copy again. A
// clean editor closes its copy for good.
func TestClosingACopyKeepsEditsNewerThanTheRequest(t *testing.T) {
	store, _, err := openNoteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copy := savedSource{ID: newNoteID(), Sequence: 3, File: "main.cicada", Revision: "saved", Source: "// typed after Save was pressed\n"}
	if _, err := store.saveSource("owner", copy); err != nil {
		t.Fatal(err)
	}
	if err := store.closeSource("owner", copy.ID, copy.File, 2, false, "", ""); err != nil {
		t.Fatal(err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err != nil || len(copies) != 1 {
		t.Fatal("a save that knew of edit 2 closed edit 3", copies, err)
	}
	if err := store.closeSource("owner", copy.ID, copy.File, 3, false, "", ""); err != nil {
		t.Fatal(err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err != nil || len(copies) != 0 {
		t.Fatal("the copy was not closed", copies, err)
	}
	if _, err := store.saveSource("owner", copy); !errors.Is(err, errSourceCheckpoint) {
		t.Fatal("a delayed checkpoint revived the closed copy", err)
	}
	copy.Sequence, copy.Source = 4, "// typed after the save landed\n"
	if _, err := store.saveSource("owner", copy); err != nil {
		t.Fatal("an edit after the save was refused", err)
	}
	if err := store.closeSource("owner", copy.ID, copy.File, 4, true, "", ""); err != nil {
		t.Fatal(err)
	}
	copy.Sequence = 5
	if _, err := store.saveSource("owner", copy); !errors.Is(err, errSourceCheckpoint) {
		t.Fatal("a copy closed for good was revived", err)
	}
}

// A copy closed for matching the file is reopened unless its window wrote
// since.
func TestReopeningACopyClosedForMatchingTheFile(t *testing.T) {
	store, _, err := openNoteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copy, err := store.saveSource("owner", savedSource{ID: newNoteID(), Sequence: 1, File: "main.cicada", Revision: "saved", Source: "// X\n"})
	if err != nil {
		t.Fatal(err)
	}
	if closed, err := store.closeSourceCopy("owner", copy); !closed || err != nil {
		t.Fatal(closed, err)
	}
	if err := store.reopenSourceCopy("owner", copy); err != nil {
		t.Fatal(err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err != nil || len(copies) != 1 || copies[0].Source != copy.Source || !copies[0].Updated.Equal(copy.Updated) {
		t.Fatal("the copy was not reopened as it was", copies, err)
	}
	if closed, err := store.closeSourceCopy("owner", copy); !closed || err != nil {
		t.Fatal(closed, err)
	}
	newer := copy
	newer.Sequence, newer.Source = 2, "// typed since\n"
	if _, err := store.saveSource("owner", newer); err != nil {
		t.Fatal(err)
	}
	if err := store.reopenSourceCopy("owner", copy); err != nil {
		t.Fatal(err)
	}
	if copies, err := store.sourceCopies("owner", copy.File); err != nil || len(copies) != 1 || copies[0].Source != newer.Source {
		t.Fatal("reopening replaced a newer edit", copies, err)
	}
}
