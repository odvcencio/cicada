package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func (e *Engine) releaseLiveIdentities(track int) {
	v := &e.voices[track]
	v.expressionGroup = expression.Group{}
	if v.poly != nil {
		v.poly.ReleaseLive()
		v.poly.DetachGroups()
	}
	if v.legacyPoly != nil {
		v.legacyPoly.NoteOffIdentity(65535)
		v.legacyPoly.DetachGroups()
	}
}

func liveCohort(id uint16) graph.Cohort {
	return graph.Cohort{NoteID: int64(id), Generation: graph.LiveGeneration}
}

func (e *Engine) liveIdentityNote(c cmd.Command) {
	v := &e.voices[c.Track]
	if v.poly == nil && v.legacyPoly == nil {
		e.fault(FaultVoiceUnsupported)
		return
	}
	kind := cmd.NoteOff
	if c.Op == cmd.OpNoteOn {
		kind = cmd.NoteOn
		if v.poly != nil {
			v.poly.Release(liveCohort(c.Index))
			if _, err := v.poly.NoteOn([4]uint8{uint8(c.Arg0)}, 1, uint8(c.Arg0>>8), false, liveCohort(c.Index)); err != nil {
				e.fault(FaultPolyNote)
				return
			}
		} else {
			v.legacyPoly.NoteOnIdentity(uint8(c.Arg0), uint8(c.Arg0>>8), c.Index)
		}
	} else if v.poly != nil {
		if c.Index == 65535 {
			v.poly.ReleaseLive()
		} else {
			v.poly.Release(liveCohort(c.Index))
		}
	} else {
		v.legacyPoly.NoteOffIdentity(c.Index)
	}
	e.emit(cmd.Message{Kind: kind, Track: c.Track, A: uint16(uint8(c.Arg0)), B: uint32(c.Index), Tick: e.transport.Tick()})
}

func (e *Engine) liveIdentityExpression(track uint8, id uint16, pitch, pressure, timbre float32) {
	v := &e.voices[track]
	if v.poly != nil {
		v.poly.SetExpression(liveCohort(id), seq.Expression{Set: true, PitchCents: pitch, Pressure: pressure, Timbre: timbre})
	} else {
		v.legacyPoly.NoteExpressionIdentity(id, pitch, pressure, timbre)
	}
}
