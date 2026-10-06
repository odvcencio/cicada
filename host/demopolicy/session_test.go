package demopolicy

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func testSession(t *testing.T) *Session {
	t.Helper()
	s, err := NewSession([]byte("seed"), func(source []byte) error {
		if string(source) == "bad" {
			return errors.New("invalid score")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionIsolationUndoRedoAndReloadReset(t *testing.T) {
	a, b := testSession(t), testSession(t)
	initial := a.Snapshot()
	if err := a.Apply(initial.Revision, []byte("edit")); err != nil {
		t.Fatal(err)
	}
	if string(b.Snapshot().Source) != "seed" || b.Snapshot().CanUndo {
		t.Fatal("another visitor's session changed")
	}
	if err := a.Undo(a.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if string(a.Snapshot().Source) != "seed" {
		t.Fatal("undo")
	}
	if a.Snapshot().Revision == initial.Revision {
		t.Fatal("undo reused stale revision")
	}
	if err := a.Apply(initial.Revision, []byte("stale")); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale edit: %v", err)
	}
	if err := a.Redo(a.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if string(a.Snapshot().Source) != "edit" {
		t.Fatal("redo")
	}
	if err := a.Reset(a.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if string(a.Snapshot().Source) != "seed" || a.Snapshot().CanUndo || a.Snapshot().CanRedo {
		t.Fatal("reset leaked history")
	}
	reloaded := testSession(t)
	if reloaded.Snapshot().CanUndo || string(reloaded.Snapshot().Source) != "seed" {
		t.Fatal("reload restored a prior session")
	}
}

func TestRejectedSourceIsAtomicAndHistoryIsBounded(t *testing.T) {
	s := testSession(t)
	before := s.Snapshot()
	for _, source := range [][]byte{[]byte("bad"), {0xff}, bytes.Repeat([]byte("x"), MaxSourceBytes+1)} {
		if err := s.Apply(before.Revision, source); err == nil {
			t.Fatal("invalid source accepted")
		}
		if after := s.Snapshot(); after.Revision != before.Revision || after.CanUndo || after.CanRedo {
			t.Fatal("rejected write changed state")
		}
	}
	for i := 0; i < MaxHistoryEntries+10; i++ {
		if err := s.Apply(s.Snapshot().Revision, []byte(fmt.Sprintf("edit %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	undos := 0
	for s.Snapshot().CanUndo {
		if err := s.Undo(s.Snapshot().Revision); err != nil {
			t.Fatal(err)
		}
		undos++
	}
	if undos != MaxHistoryEntries {
		t.Fatalf("history retained %d changes", undos)
	}
	if err := s.Apply(s.Snapshot().Revision, []byte("new branch")); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().CanRedo {
		t.Fatal("new edit retained abandoned redo")
	}
}

func TestSessionDoesNotAliasInputSnapshotsOrValidator(t *testing.T) {
	seed := []byte("seed")
	s, err := NewSession(seed, func(source []byte) error { source[0] = 'X'; return nil })
	if err != nil {
		t.Fatal(err)
	}
	seed[0] = 'Y'
	snap := s.Snapshot()
	snap.Source[0] = 'Z'
	edit := []byte("edit")
	if err := s.Apply(s.Snapshot().Revision, edit); err != nil {
		t.Fatal(err)
	}
	edit[0] = 'Y'
	if string(s.Snapshot().Source) != "edit" {
		t.Fatal("source aliased input or validator")
	}
	if err := s.Reset(s.Snapshot().Revision); err != nil {
		t.Fatal(err)
	}
	if string(s.Snapshot().Source) != "seed" {
		t.Fatal("seed aliased caller")
	}
}
