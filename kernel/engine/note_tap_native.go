//go:build !tinygo && !js

package engine

import "m31labs.dev/cicada/kernel/seq"

// NoteTap receives every note transition the engine plays from a pattern: a
// seq.NoteOn when a note starts (drum hits included, with the lane in Note) and
// a seq.NoteOff when a held note is released. The event is the scheduler's own,
// so Notes, NoteCount, Velocity, Tick and NoteID are exactly what sounded.
// The tap runs on the render goroutine, in order, and must not retain the
// engine. Host builds only: the core TinyGo and browser builds compile it out.
type NoteTap func(track uint8, event seq.Event)

type noteTap struct{ fn NoteTap }

func (t *noteTap) fire(track int, event seq.Event) {
	if t.fn != nil {
		t.fn(uint8(track), event)
	}
}

// SetNoteTap installs or, with nil, removes the note tap.
func (e *Engine) SetNoteTap(fn NoteTap) { e.tap.fn = fn }
