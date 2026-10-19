package control

import "m31labs.dev/cicada/host/midi"

// NoteTime keeps a native input's position separate from UI delivery time.
type NoteTime struct {
	Tick  int64
	Input *midi.NoteTiming
}

func NativeNoteTime(event midi.Event, epoch uint64, fallbackTick int64) (NoteTime, bool) {
	p := event.Position
	if p != nil && (p.Stale || p.Epoch != epoch || !p.Playing) {
		return NoteTime{}, false
	}
	t := NoteTime{Tick: fallbackTick}
	if p != nil && p.Valid {
		t.Tick = p.Tick
	}
	if event.HostNanos > 0 {
		t.Input = &midi.NoteTiming{HostNanos: event.HostNanos, Source: event.TimeSource, Estimated: p == nil || !p.Valid}
		if p != nil {
			t.Input.ClockEpoch = p.Epoch
		}
	}
	return t, true
}

// End closes a captured note. Forced releases keep the original onset and
// explicitly mark their missing native release time instead of inventing one.
func (n *Note) End(at NoteTime) {
	n.EndTick = max(n.Tick, at.Tick)
	if len(n.Expressions) > 0 {
		n.EndTick = max(n.EndTick, n.Expressions[len(n.Expressions)-1].Tick)
	}
	if n.Input == nil {
		return
	}
	if at.Input != nil && at.Input.ClockEpoch == n.Input.ClockEpoch && at.Input.HostNanos >= n.Input.HostNanos {
		n.Input.EndHostNanos, n.Input.EndSource = at.Input.HostNanos, at.Input.Source
		n.Input.Estimated = n.Input.Estimated || at.Input.Estimated
	} else {
		n.Input.Estimated = true
	}
}
