package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
)

func liveGroup(id uint16) expression.Group {
	return expression.Group{ID: id, Generation: graph.LiveGeneration}
}

func (e *Engine) bindLiveGroup(c cmd.Command) {
	v := &e.voices[c.Track]
	group, neutral := liveGroup(uint16(c.Arg0)), expression.Controls{Timbre: .5}
	if v.poly != nil {
		v.poly.BindGroup(liveCohort(c.Index), group, neutral)
	} else if v.legacyPoly != nil {
		v.legacyPoly.BindGroupIdentity(c.Index, group, neutral)
	} else if v.noteActive && v.noteIdentity == c.Index && (v.kind == VoiceGraph || v.kind == VoiceAcid) {
		v.expressionGroup = group
		e.setGroupControls(int(c.Track), group, neutral)
	}
}

// LiveGroupActive is read by the native audio reader, never by a control
// goroutine. A host may reuse a group slot only after its last voice is gone.
func (e *Engine) LiveGroupActive(track uint8, id uint16) bool {
	if int(track) >= e.tracks || id == 0 {
		return false
	}
	v, group := &e.voices[track], liveGroup(id)
	if v.poly != nil {
		return v.poly.GroupActive(group)
	}
	if v.legacyPoly != nil {
		return v.legacyPoly.GroupActive(group)
	}
	return v.expressionGroup == group
}
