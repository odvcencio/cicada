package collab

// Replica retains this editor's unacknowledged operations and undo history.
type Replica struct {
	Actor     string      `json:"actor"`
	Document  *Document   `json:"document"`
	Pending   []Operation `json:"pending,omitempty"`
	UndoStack []uint64    `json:"undo,omitempty"`
	RedoStack []uint64    `json:"redo,omitempty"`
}

func (r *Replica) Edit(value string) error {
	old, _ := r.Document.Visible()
	if old == value {
		return nil
	}
	op, err := r.Document.Replace(r.Actor, value)
	if err != nil {
		return err
	}
	r.Pending = append(r.Pending, op)
	r.UndoStack = append(r.UndoStack, op.Seq)
	r.RedoStack = nil
	return nil
}
func (r *Replica) Undo() error { return r.toggle(true) }
func (r *Replica) Redo() error { return r.toggle(false) }
func (r *Replica) toggle(undo bool) error {
	from, to := &r.UndoStack, &r.RedoStack
	if !undo {
		from, to = to, from
	}
	if len(*from) == 0 {
		return nil
	}
	target := (*from)[len(*from)-1]
	op, err := r.Document.Toggle(r.Actor, target, undo)
	if err != nil {
		return err
	}
	*from = (*from)[:len(*from)-1]
	*to = append(*to, target)
	r.Pending = append(r.Pending, op)
	return nil
}
func (r *Replica) Receive(ops []Operation) error {
	if err := r.Document.Merge(ops); err != nil {
		return err
	}
	seen := map[ID]bool{}
	for _, op := range ops {
		seen[key(op.Actor, op.Seq)] = true
	}
	remaining := r.Pending[:0]
	for _, op := range r.Pending {
		if !seen[key(op.Actor, op.Seq)] {
			remaining = append(remaining, op)
		}
	}
	r.Pending = remaining
	return nil
}
