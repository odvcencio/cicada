package smf

import (
	"fmt"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
)

func validateSharedMIDIRange(note Note, group SharedGesture) error {
	member := seq.Expression{Timbre: .5}
	shared := expression.Controls{Timbre: .5}
	mi, gi := 0, 0
	for gi < len(group.Points) && group.Points[gi].Tick <= note.Tick {
		shared = group.Points[gi].Value
		gi++
	}
	at, end := note.Tick, note.Tick+note.Dur
	check := func() error {
		bend := member.PitchCents + shared.PitchCents
		if bend < -9600 || bend > 9600 {
			return fmt.Errorf("shared and member bend exceed the unclipped 96-semitone range; reduce the bend or render WAV")
		}
		return nil
	}
	for {
		for mi < len(note.Gesture.Points) && note.Tick+note.Gesture.Points[mi].Tick <= at {
			member = note.Gesture.Points[mi].Params
			mi++
		}
		for gi < len(group.Points) && group.Points[gi].Tick <= at {
			shared = group.Points[gi].Value
			gi++
		}
		if err := check(); err != nil {
			return err
		}
		next := end + 1
		if mi < len(note.Gesture.Points) {
			next = min(next, note.Tick+note.Gesture.Points[mi].Tick)
		}
		if gi < len(group.Points) {
			next = min(next, group.Points[gi].Tick)
		}
		if next > end {
			break
		}
		at = next
	}
	for ; gi < len(group.Points); gi++ {
		shared = group.Points[gi].Value
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// A continuous selection shares one master lane across loop iterations. A new
// selection has a new lane, so old release tails retain the outgoing controls.
type performedMIDIState struct {
	performance *seq.Performance
	origin, end int64
	base        int
}

func appendSharedRange(track *TrackChunk, p *seq.Performance, origin, from, to int64) int {
	if len(p.Groups) == 0 {
		return 0
	}
	state := track.performedGroups
	if state == nil || state.performance != p || state.origin != origin || state.end != from {
		state = &performedMIDIState{performance: p, origin: origin, end: from, base: len(track.Groups)}
		track.performedGroups = state
		for _, group := range p.Groups {
			track.Groups = append(track.Groups, SharedGesture{Name: group.Name, Points: []seq.GroupPoint{{Tick: from, Value: group.At((from - origin) % p.LengthTicks)}}})
		}
	}
	for i, group := range p.Groups {
		lane := &track.Groups[int(state.base)+i]
		appendPoint := func(tick int64, value expression.Controls) {
			if tick < from || tick >= to {
				return
			}
			point := seq.GroupPoint{Tick: tick, Value: value}
			if len(lane.Points) > 0 && lane.Points[len(lane.Points)-1].Tick == tick {
				lane.Points[len(lane.Points)-1] = point
			} else {
				lane.Points = append(lane.Points, point)
			}
		}
		for iteration := max(int64(0), (from-origin)/p.LengthTicks); origin+iteration*p.LengthTicks < to; iteration++ {
			base := origin + iteration*p.LengthTicks
			if group.Points[0].Tick > 0 {
				appendPoint(base, expression.Controls{Timbre: .5})
			}
			for _, point := range group.Points {
				appendPoint(base+point.Tick, point.Value)
			}
		}
	}
	state.end = to
	return state.base
}
