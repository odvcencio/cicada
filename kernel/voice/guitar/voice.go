// Package guitar adapts the experimental physical model and amp to score playback.
// It is a research prototype without listening acceptance.
package guitar

import (
	"math"

	"m31labs.dev/cicada/experimental/expressive"
	"m31labs.dev/cicada/kernel"
)

type Error string

func (e Error) Error() string { return string(e) }

// Params uses semitones for Bend, cents for Vibrato and ratios for the rest.
// All six continuous controls use the model's 8 ms exponential smoothing.
type Params struct {
	Bend, Vibrato, Brightness, Damping, Pickup, Drive float64
}

func DefaultParams() Params { return Params{Brightness: .7, Pickup: .22} }

func (p Params) Value(id kernel.ParamID) float64 {
	switch id {
	case kernel.ParamGuitarBend:
		return p.Bend
	case kernel.ParamGuitarVibrato:
		return p.Vibrato
	case kernel.ParamGuitarBrightness:
		return p.Brightness
	case kernel.ParamGuitarDamping:
		return p.Damping
	case kernel.ParamGuitarPickup:
		return p.Pickup
	case kernel.ParamGuitarDrive:
		return p.Drive
	}
	return 0
}

func (p *Params) Set(id kernel.ParamID, value float64) error {
	if id < kernel.ParamGuitarBend || id > kernel.ParamGuitarDrive {
		return Error("unknown guitar control")
	}
	spec := kernel.Params[id]
	if math.IsNaN(value) || math.IsInf(value, 0) || float32(value) < spec.Min || float32(value) > spec.Max {
		return Error("guitar control outside its registry range")
	}
	switch id {
	case kernel.ParamGuitarBend:
		p.Bend = value
	case kernel.ParamGuitarVibrato:
		p.Vibrato = value
	case kernel.ParamGuitarBrightness:
		p.Brightness = value
	case kernel.ParamGuitarDamping:
		p.Damping = value
	case kernel.ParamGuitarPickup:
		p.Pickup = value
	case kernel.ParamGuitarDrive:
		p.Drive = value
	}
	return nil
}

func (p Params) Validate() error {
	for id := kernel.ParamGuitarBend; id <= kernel.ParamGuitarDrive; id++ {
		if err := p.Set(id, p.Value(id)); err != nil {
			return err
		}
	}
	return nil
}

// Voice allocates its delay storage once, before the audio callback starts.
type Voice struct {
	model  *expressive.Guitar
	params Params
	pitch  float64
	gate   bool
}

func New(sampleRate int, params Params) (*Voice, error) {
	if sampleRate < 8000 || sampleRate > 192000 {
		return nil, Error("unsupported guitar sample rate")
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	v := &Voice{model: expressive.NewGuitar(sampleRate), params: params, pitch: 110}
	v.Reset()
	return v, nil
}

func (v *Voice) expression() expressive.Expression {
	return expressive.Expression{PitchHz: v.pitch * math.Exp2(v.params.Bend/12),
		Vibrato: v.params.Vibrato, Brightness: v.params.Brightness,
		Damping: v.params.Damping, Position: v.params.Pickup, Drive: v.params.Drive}
}

func (v *Voice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	v.pitch = 440 * math.Exp2((float64(note)-69)/12)
	e := v.expression()
	v.model.SetExpression(e)
	if !slide || !v.gate {
		v.model.NoteOn(e.PitchHz, float64(velocity)/127)
	}
	v.gate = true
}

func (v *Voice) NoteOff()       { v.gate = false; v.model.NoteOff() }
func (v *Voice) Next() float32  { return float32(v.model.Next()) }
func (v *Voice) Params() Params { return v.params }

func (v *Voice) SetParam(id kernel.ParamID, value float64) error {
	if err := v.params.Set(id, value); err != nil {
		return err
	}
	v.model.SetExpression(v.expression())
	return nil
}

func (v *Voice) Reset() {
	v.gate = false
	v.pitch = 110
	v.model.SetExpression(v.expression())
	v.model.Reset()
}
