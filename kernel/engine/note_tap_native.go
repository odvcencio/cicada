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

// tapRelease reports the release of the track's playing pattern note at tick.
// Call it before the note's identity is cleared. It does nothing when no
// pattern note is playing, so a release already tapped is not repeated.
func (e *Engine) tapRelease(track int, tick int64) {
	if e.tap.fn == nil {
		return
	}
	if id := e.patterns[track].playingNote; id != 0 {
		e.tap.fn(uint8(track), seq.Event{Kind: seq.NoteOff, NoteID: id, Tick: tick, Track: uint8(track)})
	}
}

// SetNoteTap installs or, with nil, removes the note tap.
func (e *Engine) SetNoteTap(fn NoteTap) { e.tap.fn = fn }
