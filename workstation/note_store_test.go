package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/workstation/control"
)

func TestSavedSharedControlsRetainTailAndCombinedBudgetAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedNoteFixture()
	note := &saved.Takes[0].Notes[0]
	note.Group = 1
	note.Expressions = []control.NoteExpression{{Tick: note.Tick, PitchCents: 25, Timbre: .5}}
	group := control.RecordedGroup{ID: 42, Device: "native:surface:g0", Channel: 0}
	for index := range control.MaxExpressionPoints - 1 {
		group.Points = append(group.Points, control.NoteExpression{Tick: note.Tick + int64(index), PitchCents: float64(index%9600) + .125, Pressure: .123456789, Timbre: .987654321})
	}
	saved.Takes[0].Groups = []control.RecordedGroup{group}
	if _, err = store.save("player", saved); err != nil {
		t.Fatal(err)
	}
	store, _, err = openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.get("player", saved.ID)
	if err != nil || restored.Version != 2 || !reflect.DeepEqual(restored.Takes, saved.Takes) {
		t.Fatal("private recovery changed shared controls or member data", err)
	}
	if restored.Takes[0].Notes[0].EndTick != 4110 || restored.Takes[0].Groups[0].Points[len(group.Points)-1].Tick <= 4110 {
		t.Fatal("tail capture stretched a gate or lost its final movement")
	}
	saved.Sequence++
	saved.Takes[0].Groups[0].Points = append(saved.Takes[0].Groups[0].Points, group.Points[len(group.Points)-1])
	if _, err = store.save("player", saved); err == nil {
		t.Fatal("combined budget overflow replaced the recoverable take")
	}
	unchanged, err := store.get("player", saved.ID)
	if err != nil || !reflect.DeepEqual(unchanged.Takes, restored.Takes) {
		t.Fatal("refused checkpoint damaged the saved performance", err)
	}
}

func TestSavedChannelControlsUseVersionThreeAndSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	store, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedNoteFixture()
	saved.Takes[0].Notes[0].Group = 1
	saved.Takes[0].Groups = []control.RecordedGroup{{ID: 7, Device: "keys", Channel: 2, Kind: midi.SharedChannelKind, Points: []control.NoteExpression{{Tick: 3840, Timbre: .5}, {Tick: 8000, PitchCents: 200, Pressure: .8, Timbre: .9}}}}
	written, err := store.save("player", saved)
	if err != nil || written.Version != 3 {
		t.Fatal("ordinary controls were written in an ambiguous old format", written.Version, err)
	}
	store, _, err = openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.get("player", saved.ID)
	if err != nil || restored.Version != 3 || !reflect.DeepEqual(restored.Takes, saved.Takes) {
		t.Fatal("restart changed channel controls", err)
	}
	saved.Sequence++
	saved.Takes[0].Groups[0].Kind = "unknown"
	if _, err := store.save("player", saved); err == nil {
		t.Fatal("unknown controls replaced the recoverable take")
	}
	unchanged, err := store.get("player", saved.ID)
	if err != nil || !reflect.DeepEqual(unchanged.Takes, restored.Takes) {
		t.Fatal("refused metadata damaged the saved take", err)
	}
}

func savedNoteFixture() savedNotes {
	return savedNotes{ID: strings.Repeat("a", 32), Sequence: 1, Revision: "score-at-recording", State: "recording", Takes: []control.Recording{{Track: "keys", Pattern: "phrase", Notes: []control.Note{{Tick: 3857, EndTick: 4110, Note: 64, Velocity: 105, EndEstimated: true, Input: &midi.NoteTiming{HostNanos: 1000000, Source: "source", ClockEpoch: 7, Estimated: true}}}}}}
}

func TestSavedNotesRetainFullGestureBudgetAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedNoteFixture()
	note := &saved.Takes[0].Notes[0]
	note.NoteID, note.Channel = 42, 9
	for i := range control.MaxExpressionPoints {
		note.AddExpression(control.NoteExpression{Tick: note.Tick + int64(i), PitchCents: float64(i%9600) + .125, Pressure: .123456789, Timbre: .987654321})
	}
	if _, err = store.save("player", saved); err != nil {
		t.Fatal(err)
	}
	store, _, err = openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.get("player", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := got.Takes[0].Notes[0]
	if len(n.Expressions) != control.MaxExpressionPoints || n.Expressions[len(n.Expressions)-1] != note.Expressions[len(note.Expressions)-1] || n.NoteID != 42 || n.Channel != 9 {
		t.Fatal("recovery changed gesture data")
	}
	note.AddExpression(control.NoteExpression{Tick: note.EndTick, Timbre: .5})
	saved.Sequence++
	if _, err = store.save("player", saved); err == nil {
		t.Fatal("over-budget raw take replaced saved performance")
	}
}

func TestSavedNotesSurviveRestartAndRejectLateWriters(t *testing.T) {
	dir := t.TempDir()
	s, key, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const owner = "private-workspace-session"
	first := savedNoteFixture()
	if _, err = s.save(owner, first); err != nil {
		t.Fatal(err)
	}
	second, key2, err := openNoteStore(dir)
	if err != nil || key != key2 {
		t.Fatalf("restart rotated identity: %v", err)
	}
	got, err := second.get(owner, first.ID)
	if err != nil || got.Takes[0].Notes[0].Input.HostNanos != 1000000 || !got.Takes[0].Notes[0].EndEstimated {
		t.Fatalf("recovery changed original timing: %+v %v", got, err)
	}
	if _, err = second.get("another-session", first.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("another session read private notes")
	}
	got.Sequence, got.State = 2, "ready"
	if _, err = second.save(owner, got); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, notePath(owner, first.ID))
	ready, _ := os.ReadFile(path)
	first.Sequence = 100
	if _, err = s.save(owner, first); !errors.Is(err, errNoteCheckpoint) {
		t.Fatal("late recording checkpoint replaced a finished take")
	}
	got.Sequence = 101
	got.Takes[0].Notes[0].Note = 65
	if _, err = s.save(owner, got); !errors.Is(err, errNoteCheckpoint) {
		t.Fatal("finished performance was silently rewritten")
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(ready, unchanged) {
		t.Fatal("refused update changed saved data")
	}
	if err = s.archive(owner, first.ID, "discarded"); err != nil {
		t.Fatal(err)
	}
	if pending, err := second.pending(owner); err != nil || len(pending) != 0 {
		t.Fatalf("discarded take revived: %v %v", pending, err)
	}
	archived, err := second.get(owner, first.ID)
	if err != nil || len(archived.Takes[0].Notes) != 1 || archived.State != "discarded" {
		t.Fatal("archive erased the original performance")
	}
	if _, err = s.save(owner, first); !errors.Is(err, errNoteCheckpoint) {
		t.Fatal("late writer revived discarded take")
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("handled performance still enters recovery scan")
	}
	// A process can stop after the closed receipt is durable but before
	// removing the old checkpoint. That tail must never resurrect a take.
	if err = os.WriteFile(path, ready, 0600); err != nil {
		t.Fatal(err)
	}
	if pending, err := second.pending(owner); err != nil || len(pending) != 0 {
		t.Fatal("interrupted archive revived old checkpoint")
	}
}

func TestSavedNotesPreserveUnreadableDataAndConfinePaths(t *testing.T) {
	dir := t.TempDir()
	s, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const owner = "private-owner"
	first := savedNoteFixture()
	if _, err = s.save(owner, first); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, notePath(owner, first.ID))
	broken := []byte(`{"version":1,"takes":[`)
	if err = os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.save(owner, first); err == nil {
		t.Fatal("unreadable data was overwritten")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, broken) {
		t.Fatal("failed read changed original bytes")
	}
	if err = os.WriteFile(filepath.Join(dir, "session.key"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = openNoteStore(dir); err == nil {
		t.Fatal("invalid key was silently rotated")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "session.key")); string(data) != "partial" {
		t.Fatal("invalid key was destroyed")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err = os.WriteFile(outside, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Skip("symlink unavailable", err)
	}
	if _, err = s.save(owner, first); err == nil {
		t.Fatal("escaped recovery root")
	}
	if data, _ := os.ReadFile(outside); string(data) != "unrelated" {
		t.Fatal("changed an unrelated file")
	}
}

func TestSavedNotesCoordinateConcurrentStores(t *testing.T) {
	dir := t.TempDir()
	a, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := uint64(1); i <= 24; i++ {
		wg.Add(1)
		go func(sequence uint64) {
			defer wg.Done()
			saved := savedNoteFixture()
			saved.Sequence = sequence
			store := a
			if sequence%2 == 0 {
				store = b
			}
			if _, err := store.save("same-owner", saved); err != nil && !errors.Is(err, errNoteCheckpoint) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := a.get("same-owner", strings.Repeat("a", 32))
	if err != nil || got.Sequence != 24 {
		t.Fatalf("concurrent update lost latest complete checkpoint: %+v %v", got, err)
	}
}
