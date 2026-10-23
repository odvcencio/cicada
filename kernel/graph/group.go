package graph

import "m31labs.dev/cicada/kernel/expression"

// DetachGroups freezes the last resolved shared value without truncating audio
// releases. It is used by transport Stop before any later input can arrive.
func (p *Pool) DetachGroups() {
	for i := range p.slots {
		p.slots[i].group = expression.Group{}
	}
}
func (p *Poly) DetachGroups() {
	for i := range p.slots {
		p.slots[i].group = expression.Group{}
	}
}

func (p *Poly) BindGroupNote(note uint8, group expression.Group, controls expression.Controls) bool {
	if group.ID == 0 {
		return false
	}
	slot := -1
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity == 0 && s.note == note && (slot < 0 || s.age > p.slots[slot].age) {
			slot = i
		}
	}
	if slot < 0 {
		return false
	}
	p.slots[slot].group = group
	p.slots[slot].voice.SetGroupControls(controls)
	return true
}

func (p *Poly) SetExpressionNote(note uint8, params expression.Params) bool {
	updated := false
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity == 0 && s.note == note {
			s.voice.SetExpression(params)
			updated = true
		}
	}
	return updated
}

func (p *Pool) BindGroup(cohort Cohort, group expression.Group, controls expression.Controls) bool {
	if group.ID == 0 {
		return false
	}
	bound := false
	for i := range p.slots {
		s := &p.slots[i]
		if s.gated && s.cohort == cohort {
			s.group = group
			s.voice.SetGroupControls(controls)
			bound = true
		}
	}
	return bound
}

// Group controls follow gated and releasing voices, while ordinary per-note
// updates still require a held gate. A stolen slot has a new group binding.
func (p *Pool) SetGroupControls(group expression.Group, controls expression.Controls) bool {
	if group.ID == 0 {
		return false
	}
	updated := false
	for i := range p.slots {
		s := &p.slots[i]
		if (s.gated || s.release > 0) && s.group == group {
			s.voice.SetGroupControls(controls)
			updated = true
		}
	}
	return updated
}

func (p *Pool) GroupActive(group expression.Group) bool {
	if group.ID == 0 {
		return false
	}
	for i := range p.slots {
		s := &p.slots[i]
		if (s.gated || s.release > 0) && s.group == group {
			return true
		}
	}
	return false
}

func (p *Poly) BindGroupIdentity(id uint16, group expression.Group, controls expression.Controls) bool {
	if id == 0 || group.ID == 0 {
		return false
	}
	bound := false
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity == id {
			s.group = group
			s.voice.SetGroupControls(controls)
			bound = true
		}
	}
	return bound
}

func (p *Poly) SetGroupControls(group expression.Group, controls expression.Controls) bool {
	if group.ID == 0 {
		return false
	}
	updated := false
	for i := range p.slots {
		s := &p.slots[i]
		if s.active && s.group == group {
			s.voice.SetGroupControls(controls)
			updated = true
		}
	}
	return updated
}

func (p *Poly) GroupActive(group expression.Group) bool {
	if group.ID == 0 {
		return false
	}
	for i := range p.slots {
		s := &p.slots[i]
		if s.active && s.group == group {
			return true
		}
	}
	return false
}
