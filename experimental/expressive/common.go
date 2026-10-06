// Package expressive contains research voices, independent of Cicada's instrument ABI.
// Instances have a single audio-thread owner. Controls and Next must not race.
package expressive

import "math"

// Expression controls an already sounding voice. PitchHz is absolute; Vibrato is
// depth in cents. Other fields are normalized 0..1. SetExpression copies values.
// Unsupported/nonfinite controls are sanitized by each voice.
type Expression struct{ PitchHz, Pressure, Position, Brightness, Vibrato, Drive, Damping float64 }
type Voice interface {
	NoteOn(hz, velocity float64)
	NoteOff()
	SetExpression(Expression)
	Next() float64
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func clamp(x, lo, hi float64) float64 {
	if !finite(x) {
		return lo
	}
	return math.Max(lo, math.Min(hi, x))
}

type delay struct {
	data []float64
	pos  int
}

func (d *delay) init(n int) { d.data = make([]float64, n); d.pos = 0 }
func (d *delay) read(n float64) float64 {
	n = clamp(n, 1, float64(len(d.data)-2))
	p := float64(d.pos) - n
	for p < 0 {
		p += float64(len(d.data))
	}
	i := int(p)
	f := p - float64(i)
	return d.data[i]*(1-f) + d.data[(i+1)%len(d.data)]*f
}
func (d *delay) push(x float64) { d.data[d.pos] = x; d.pos = (d.pos + 1) % len(d.data) }
