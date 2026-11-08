package graph

// SetConstant changes the value of one Constant node while the voice plays.
// An authored instrument parameter lowers to such a node, so this is how a
// scene setting, automation lane or modulation route moves it. Any other node
// is left alone.
func (v *Voice) SetConstant(node int, value float32) {
	if node >= 0 && node < int(v.program.Len) && v.program.Nodes[node].Op == Constant {
		v.program.Nodes[node].Value = value
	}
}

// SetConstant sets the node on every voice of the pool.
func (p *Pool) SetConstant(node int, value float32) {
	for i := range p.slots {
		p.slots[i].voice.SetConstant(node, value)
	}
}

// SetConstant sets the node on every voice of the legacy poly.
func (p *Poly) SetConstant(node int, value float32) {
	for i := range p.slots {
		p.slots[i].voice.SetConstant(node, value)
	}
}
