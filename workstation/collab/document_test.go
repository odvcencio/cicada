package collab

import (
	"encoding/json"
	"math/rand"
	"testing"
)

func requireText(t *testing.T, d *Document, want string) {
	t.Helper()
	got, _ := d.Visible()
	if got != want {
		t.Fatalf("text=%q want=%q", got, want)
	}
}

func TestConcurrentInsertDeleteAndUndo(t *testing.T) {
	base, _ := New("a🎵b")
	a, b := base.Clone(), base.Clone()
	insert, err := a.Splice("user-a", 2, 0, "X")
	if err != nil {
		t.Fatal(err)
	}
	del, err := b.Splice("user-b", 1, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Merge(b.Operations); err != nil {
		t.Fatal(err)
	}
	if err := b.Merge(a.Operations); err != nil {
		t.Fatal(err)
	}
	requireText(t, a, "aXb")
	requireText(t, b, "aXb")
	if _, err := a.Toggle("user-a", insert.Seq, true); err != nil {
		t.Fatal(err)
	}
	requireText(t, a, "ab")
	if _, err := a.Toggle("user-b", del.Seq, true); err != nil {
		t.Fatal(err)
	}
	requireText(t, a, "a🎵b")
	if _, err := a.Toggle("user-a", insert.Seq, false); err != nil {
		t.Fatal(err)
	}
	requireText(t, a, "a🎵Xb")
}

func TestUndoDoesNotReviveAnotherUsersDelete(t *testing.T) {
	base, _ := New("abc")
	a, b := base.Clone(), base.Clone()
	da, _ := a.Splice("user-a", 1, 1, "")
	_, _ = b.Splice("user-b", 1, 1, "")
	if err := a.Merge(b.Operations); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Toggle("user-a", da.Seq, true); err != nil {
		t.Fatal(err)
	}
	requireText(t, a, "ac")
}

func TestUndoPreservesConcurrentDescendants(t *testing.T) {
	a, _ := New("a")
	op, _ := a.Splice("user-a", 1, 0, "X")
	b := a.Clone()
	_, _ = b.Splice("user-b", 2, 0, "Y")
	if err := a.Merge(b.Operations); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Toggle("user-a", op.Seq, true)
	requireText(t, a, "aY")
}

func TestTransactionalMergeRejectsIdentityAndMissingReferences(t *testing.T) {
	d, _ := New("abc")
	before, _ := json.Marshal(d)
	for _, ops := range [][]Operation{
		{{Actor: "initial", Seq: 1, Clock: 1, Insert: "oops"}},
		{{Actor: "x", Seq: 2, Clock: 3, Insert: "x"}},
		{{Actor: "x", Seq: 1, Clock: 2, After: ID{Actor: "missing", Seq: 1}, Insert: "x"}},
		{{Actor: "x", Seq: 1, Clock: 2, Target: 1, Undo: true}},
		{{Actor: "x", Seq: 1, Clock: 1, Delete: []ID{{Actor: "initial", Seq: 1, Offset: 0}}}},
	} {
		if err := d.Merge(ops); err == nil {
			t.Fatalf("accepted invalid operations: %+v", ops)
		}
		after, _ := json.Marshal(d)
		if string(before) != string(after) {
			t.Fatal("failed merge changed the document")
		}
	}
}

func TestSeededOfflineReplicaConvergence(t *testing.T) {
	base, _ := New("🎶 shared score text ")
	a := &Replica{Actor: "a", Document: base.Clone()}
	b := &Replica{Actor: "b", Document: base.Clone()}
	rng := rand.New(rand.NewSource(73))
	for i := 0; i < 1000; i++ {
		r := a
		if i%2 != 0 {
			r = b
		}
		text, _ := r.Document.Visible()
		runes := []rune(text)
		at := rng.Intn(len(runes) + 1)
		count := 0
		if at < len(runes) && rng.Intn(3) == 0 {
			count = 1
		}
		op, err := r.Document.Splice(r.Actor, at, count, string([]rune("abc🎵")[rng.Intn(4)]))
		if err != nil {
			t.Fatal(err)
		}
		r.Pending = append(r.Pending, op)
		if i%101 == 0 {
			if err := a.Receive(b.Document.Operations); err != nil {
				t.Fatal(err)
			}
			if err := b.Receive(a.Document.Operations); err != nil {
				t.Fatal(err)
			}
		}
	}
	union := append(append([]Operation(nil), a.Document.Operations...), b.Document.Operations...)
	rng.Shuffle(len(union), func(i, j int) { union[i], union[j] = union[j], union[i] })
	if err := a.Receive(union); err != nil {
		t.Fatal(err)
	}
	if err := b.Receive(union); err != nil {
		t.Fatal(err)
	}
	aText, _ := a.Document.Visible()
	requireText(t, b.Document, aText)
	x, _ := json.Marshal(a)
	var restored Replica
	if json.Unmarshal(x, &restored) != nil {
		t.Fatal("cannot restore replica")
	}
	requireText(t, restored.Document, aText)
	if len(a.Pending) != 0 || len(b.Pending) != 0 {
		t.Fatal("acknowledged edits stayed queued")
	}
}
