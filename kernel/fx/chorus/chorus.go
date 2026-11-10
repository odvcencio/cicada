// Package chorus contains the experimental chorus DSP. It is not yet wired to
// score syntax or the engine; see the lane-2 design for the remaining gates.
package chorus

import (
	"math"

	"m31labs.dev/cicada/kernel/modulate"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrSampleRate Error = "chorus supports 44100, 48000, and 96000 Hz"
	ErrParams     Error = "chorus parameter is invalid or out of range"
)

type Params struct {
	Voices                   int
	Rate, Depth, Spread, Mix float64 // hertz, milliseconds, unit interval, unit interval
}

func DefaultParams() Params { return Params{Voices: 3, Rate: .6, Depth: 3, Spread: 1, Mix: .5} }

func (p Params) Validate() error {
	for _, value := range [...]float64{p.Rate, p.Depth, p.Spread, p.Mix} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return ErrParams
		}
	}
	if p.Voices < 1 || p.Voices > 3 || p.Rate < .05 || p.Rate > 5 || p.Depth < 0 || p.Depth > 8 || p.Spread < 0 || p.Spread > 1 || p.Mix < 0 || p.Mix > 1 {
		return ErrParams
	}
	return nil
}

type frame struct{ left, right float32 }

type Processor struct {
	rate                           float64
	params                         Params
	current                        Params
	smooth, lowpass                float64
	filteredL, filteredR           float64
	ring                           []frame
	at                             int
	slowPhase, fastPhase, fastStep [3]uint32
	fault                          bool
}

// New preallocates the complete delay history and initializes settled controls.
// Voices are fixed for the lifetime of a processor.
func New(sampleRate int, params Params) (*Processor, error) {
	if sampleRate != 44100 && sampleRate != 48000 && sampleRate != 96000 {
		return nil, ErrSampleRate
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	r := float64(sampleRate)
	p := &Processor{rate: r, params: params, current: params, smooth: 1 - math.Exp(-1/(.01*r)), lowpass: 1 - math.Exp(-2*math.Pi*6500/r), ring: make([]frame, int(math.Ceil(.028*r))+2)}
	for i, hz := range [...]float64{5.73, 6.347, 6.964} {
		p.fastStep[i] = uint32(float64(hz*(4294967296/r)) + .5)
	}
	p.Reset()
	return p, nil
}

func (p *Processor) Params() Params     { return p.params }
func (p *Processor) Fault() bool        { return p.fault }
func (p *Processor) LatencyFrames() int { return 0 }

// SetParams sets control targets; voice count is a compile-time choice.
func (p *Processor) SetParams(params Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	if params.Voices != p.params.Voices {
		return ErrParams
	}
	p.params = params
	return nil
}

// SetParamsImmediate settles controls without clearing delay or oscillator history.
func (p *Processor) SetParamsImmediate(params Params) error {
	if err := p.SetParams(params); err != nil {
		return err
	}
	p.current = params
	return nil
}

func (p *Processor) Reset() {
	clear(p.ring)
	p.at, p.filteredL, p.filteredR, p.fault = 0, 0, 0, false
	p.current = p.params
	for i := range p.slowPhase {
		phase := uint32(uint64(i) * (1 << 32) / 3)
		p.slowPhase[i], p.fastPhase[i] = phase, phase
	}
}

func (p *Processor) read(delay float64, left bool) float64 {
	whole := int(delay)
	fraction := delay - float64(whole)
	first := p.at - whole
	if first < 0 {
		first += len(p.ring)
	}
	second := first - 1
	if second < 0 {
		second += len(p.ring)
	}
	a, b := p.ring[first], p.ring[second]
	if left {
		return float64(a.left) + float64((float64(b.left)-float64(a.left))*fraction)
	}
	return float64(a.right) + float64((float64(b.right)-float64(a.right))*fraction)
}

// Process advances the delay and oscillator history even at dry mix. It does
// not allocate or call the math library in the audio callback.
func (p *Processor) Process(left, right float32) (float32, float32) {
	if p.fault {
		return 0, 0
	}
	if !finite32(left) || !finite32(right) {
		p.fault = true
		return 0, 0
	}
	p.current.Rate += float64((p.params.Rate - p.current.Rate) * p.smooth)
	p.current.Depth += float64((p.params.Depth - p.current.Depth) * p.smooth)
	p.current.Spread += float64((p.params.Spread - p.current.Spread) * p.smooth)
	p.current.Mix += float64((p.params.Mix - p.current.Mix) * p.smooth)
	if p.params.Mix == 0 && p.current.Mix < .000001 {
		p.current.Mix = 0
	}
	p.filteredL += float64((float64(left) - p.filteredL) * p.lowpass)
	p.filteredR += float64((float64(right) - p.filteredR) * p.lowpass)
	p.ring[p.at] = frame{float32(p.filteredL), float32(p.filteredR)}
	var wetL, wetR float64
	for i := 0; i < p.params.Voices; i++ {
		slow := float64(modulate.Sin(p.slowPhase[i])) / modulate.One
		fast := float64(modulate.Sin(p.fastPhase[i])) / modulate.One
		base := [...]float64{16, 17.3, 18.6}[i]
		offset := float64(p.current.Depth*slow) + float64(.65*fast)
		wetL += p.read((base+offset)*p.rate/1000, true)
		wetR += p.read((base-offset)*p.rate/1000, false)
		p.slowPhase[i] += uint32(float64(p.current.Rate*[...]float64{1, 1.205, 1.410}[i]*(4294967296/p.rate)) + .5)
		p.fastPhase[i] += p.fastStep[i]
	}
	p.at++
	if p.at == len(p.ring) {
		p.at = 0
	}
	if p.current.Mix == 0 {
		return left, right
	}
	wetL /= float64(p.params.Voices)
	wetR /= float64(p.params.Voices)
	mid := (wetL + wetR) * .5
	spread, mix := p.current.Spread, p.current.Mix
	return float32(float64((1-mix)*float64(left)) + float64(mix*(float64((1-spread)*mid)+float64(spread*wetL)))), float32(float64((1-mix)*float64(right)) + float64(mix*(float64((1-spread)*mid)+float64(spread*wetR))))
}

func finite32(v float32) bool { return v == v && v <= math.MaxFloat32 && v >= -math.MaxFloat32 }
