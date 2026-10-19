package seq

import "m31labs.dev/cicada/kernel/expression"

// ControlGroup is a shared bend/pressure/timbre lane in pattern time. Notes
// bind by its 1-based ordinal. The lane continues after their gates release.
type ControlGroup struct {
	Name   string
	Points []GroupPoint
}

type GroupPoint struct {
	Tick  int64
	Value expression.Controls
}

func validGroupName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i, c := range []byte(name) {
		letter := c >= 'a' && c <= 'z' || c == '_'
		if !letter && (i == 0 || c != '-' && (c < '0' || c > '9')) {
			return false
		}
	}
	return true
}

// At chases a lane without scanning its old history or allocating in render.
func (g *ControlGroup) At(tick int64) expression.Controls {
	lo, hi := 0, len(g.Points)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if g.Points[mid].Tick <= tick {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return expression.Controls{Timbre: .5}
	}
	return g.Points[lo-1].Value
}
