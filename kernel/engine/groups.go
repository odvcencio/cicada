package engine

import (
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
)

func (e *Engine) bindPerformedGroup(track int, note uint8, cohort graph.Cohort, group expression.Group, controls expression.Controls) {
	v := &e.voices[track]
	if v.poly != nil {
		v.poly.BindGroup(cohort, group, controls)
		return
	}
	if v.legacyPoly != nil {
		v.legacyPoly.BindGroupNote(note, group, controls)
		return
	}
	v.expressionGroup = group
	switch v.kind {
	case VoiceGraph:
		v.graph.SetGroupControls(controls)
	case VoiceAcid:
		v.acid.SetGroupControls(controls)
	}
}

func (e *Engine) setGroupControls(track int, group expression.Group, controls expression.Controls) {
	v := &e.voices[track]
	if v.poly != nil {
		v.poly.SetGroupControls(group, controls)
		return
	}
	if v.legacyPoly != nil {
		v.legacyPoly.SetGroupControls(group, controls)
		return
	}
	if v.expressionGroup != group {
		return
	}
	switch v.kind {
	case VoiceGraph:
		v.graph.SetGroupControls(controls)
	case VoiceAcid:
		v.acid.SetGroupControls(controls)
	}
}
