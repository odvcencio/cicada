// Package collab implements a replicated score draft. It runs outside the audio
// callback, in the Studio server and its Go/WASM editor.
package collab

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"unicode/utf8"
)

const MaxText = 1 << 20
const MaxOperations = 20000

// ID names an inserted rune, independently of its visible position.
type ID struct {
	Actor  string `json:"actor"`
	Seq    uint64 `json:"seq"`
	Offset int    `json:"offset"`
}

// Operation is immutable. A toggle can only change the visibility of an edit
// by the same actor. Active deletes compose: undoing one cannot undo another.
type Operation struct {
	Actor  string `json:"actor"`
	Seq    uint64 `json:"seq"`
	Clock  uint64 `json:"clock"`
	After  ID     `json:"after,omitempty"`
	Insert string `json:"insert,omitempty"`
	Delete []ID   `json:"delete,omitempty"`
	Target uint64 `json:"target,omitempty"`
	Undo   bool   `json:"undo,omitempty"`
}

type Document struct {
	Operations []Operation `json:"operations"`
}

func New(source string) (*Document, error) {
	d := &Document{}
	if source != "" {
		if err := d.Merge([]Operation{{Actor: "initial", Seq: 1, Clock: 1, Insert: source}}); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func key(actor string, seq uint64) ID { return ID{Actor: actor, Seq: seq} }

// Merge validates the complete candidate before changing the document. Replays
// are idempotent; a conflicting reuse of an operation identity is rejected.
func (d *Document) Merge(incoming []Operation) error {
	all := make(map[ID]Operation, len(d.Operations)+len(incoming))
	for _, op := range d.Operations {
		all[key(op.Actor, op.Seq)] = op
	}
	for _, op := range incoming {
		k := key(op.Actor, op.Seq)
		if old, ok := all[k]; ok && !reflect.DeepEqual(old, op) {
			return fmt.Errorf("operation identity reused")
		}
		all[k] = op
	}
	if len(all) > MaxOperations {
		return fmt.Errorf("shared draft history is full")
	}
	runes := make(map[ID]int, len(all))
	bytes := 0
	for k, op := range all {
		if op.Actor == "" || len(op.Actor) > 64 || op.Seq == 0 || op.Clock == 0 || op.Clock > 1<<53 || !utf8.ValidString(op.Insert) || len(op.Delete) > MaxText {
			return fmt.Errorf("invalid score operation")
		}
		if op.Target != 0 && (op.Insert != "" || len(op.Delete) != 0 || op.After != (ID{})) {
			return fmt.Errorf("invalid undo operation")
		}
		if op.Target == 0 && op.Undo {
			return fmt.Errorf("undo requires an edit")
		}
		runes[k] = utf8.RuneCountInString(op.Insert)
		bytes += len(op.Insert)
		if bytes > 8*MaxText {
			return fmt.Errorf("shared draft history exceeds 8 MiB")
		}
	}
	ref := func(id ID, op Operation) bool {
		parent, ok := all[key(id.Actor, id.Seq)]
		return ok && id.Offset >= 0 && id.Offset < runes[key(id.Actor, id.Seq)] && parent.Clock < op.Clock
	}
	for _, op := range all {
		if op.Seq > 1 {
			prev, ok := all[key(op.Actor, op.Seq-1)]
			if !ok || prev.Clock >= op.Clock {
				return fmt.Errorf("missing actor history")
			}
		}
		if op.After != (ID{}) && !ref(op.After, op) {
			return fmt.Errorf("missing insertion anchor")
		}
		for _, id := range op.Delete {
			if !ref(id, op) {
				return fmt.Errorf("missing deleted element")
			}
		}
		if op.Target != 0 {
			target, ok := all[key(op.Actor, op.Target)]
			if !ok || target.Target != 0 || target.Seq >= op.Seq {
				return fmt.Errorf("undo must name an earlier edit by this user")
			}
		}
	}
	candidate := &Document{Operations: make([]Operation, 0, len(all))}
	for _, op := range all {
		candidate.Operations = append(candidate.Operations, op)
	}
	sort.Slice(candidate.Operations, func(i, j int) bool {
		a, b := candidate.Operations[i], candidate.Operations[j]
		if a.Clock != b.Clock {
			return a.Clock < b.Clock
		}
		if a.Actor != b.Actor {
			return a.Actor < b.Actor
		}
		return a.Seq < b.Seq
	})
	text, _ := candidate.Visible()
	if len(text) > MaxText {
		return fmt.Errorf("shared score exceeds 1 MiB")
	}
	// Own incoming slices: callers must not be able to mutate committed edits.
	for i := range candidate.Operations {
		candidate.Operations[i].Delete = append([]ID(nil), candidate.Operations[i].Delete...)
	}
	d.Operations = candidate.Operations
	return nil
}

// Visible returns text and stable rune identities. Tombstoned parents remain
// in the traversal, preserving concurrent insertions anchored to deleted text.
func (d *Document) Visible() (string, []ID) {
	active := map[ID]bool{}
	for _, op := range d.Operations {
		if op.Target == 0 {
			active[key(op.Actor, op.Seq)] = true
		} else {
			active[key(op.Actor, op.Target)] = !op.Undo
		}
	}
	type node struct {
		id    ID
		char  rune
		clock uint64
	}
	children := map[ID][]node{}
	deleted := map[ID]bool{}
	for _, op := range d.Operations {
		if active[key(op.Actor, op.Seq)] {
			for _, id := range op.Delete {
				deleted[id] = true
			}
		}
		after := op.After
		for i, r := range []rune(op.Insert) {
			id := ID{Actor: op.Actor, Seq: op.Seq, Offset: i}
			children[after] = append(children[after], node{id, r, op.Clock})
			after = id
		}
	}
	for id := range children {
		sort.Slice(children[id], func(i, j int) bool {
			a, b := children[id][i], children[id][j]
			if a.clock != b.clock {
				return a.clock > b.clock
			}
			return a.id.Actor > b.id.Actor
		})
	}
	var text []rune
	var ids []ID
	// Iterative traversal also bounds stack use for long scores.
	stack := append([]node(nil), children[ID{}]...)
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if active[key(n.id.Actor, n.id.Seq)] && !deleted[n.id] {
			text = append(text, n.char)
			ids = append(ids, n.id)
		}
		kids := children[n.id]
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, kids[i])
		}
	}
	return string(text), ids
}

func (d *Document) next(actor string) Operation {
	op := Operation{Actor: actor, Seq: 1, Clock: 1}
	for _, other := range d.Operations {
		if other.Actor == actor && other.Seq >= op.Seq {
			op.Seq = other.Seq + 1
		}
		if other.Clock >= op.Clock {
			op.Clock = other.Clock + 1
		}
	}
	return op
}

func (d *Document) Splice(actor string, index, count int, insert string) (Operation, error) {
	_, ids := d.Visible()
	if index < 0 || count < 0 || index > len(ids) || count > len(ids)-index {
		return Operation{}, fmt.Errorf("score edit is out of range")
	}
	op := d.next(actor)
	if index > 0 {
		op.After = ids[index-1]
	}
	op.Insert = insert
	op.Delete = append([]ID(nil), ids[index:index+count]...)
	return op, d.Merge([]Operation{op})
}

// Replace computes one contiguous rune splice, preserving Unicode and anchors.
func (d *Document) Replace(actor, value string) (Operation, error) {
	old, _ := d.Visible()
	a, b := []rune(old), []rune(value)
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	return d.Splice(actor, start, endA-start, string(b[start:endB]))
}

func (d *Document) Toggle(actor string, target uint64, undo bool) (Operation, error) {
	op := d.next(actor)
	op.Target = target
	op.Undo = undo
	return op, d.Merge([]Operation{op})
}

func (d *Document) Clone() *Document {
	data, _ := json.Marshal(d)
	var out Document
	_ = json.Unmarshal(data, &out)
	return &out
}
