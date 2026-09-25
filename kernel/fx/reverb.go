package fx

import "math"

// ReverbParams describes the bounded eight-line feedback delay network.
// Mix is normally one for a mixer return.
type ReverbParams struct {
	Size       float64
	DecaySec   float64
	DampHz     float64
	HighpassHz float64
	PredelayMs float64
	Mix        float64
}

func DefaultReverbParams() ReverbParams {
	return ReverbParams{Size: 1, DecaySec: 2.4, DampHz: 8_000, HighpassHz: 120, Mix: 1}
}

func (p ReverbParams) Validate() error {
	if !finite(p.Size) || !finite(p.DecaySec) || !finite(p.DampHz) || !finite(p.HighpassHz) || !finite(p.PredelayMs) || !finite(p.Mix) ||
		p.Size < .5 || p.Size > 1.5 || p.DecaySec < .3 || p.DecaySec > 12 || p.DampHz < 2_000 || p.DampHz > 16_000 ||
		p.HighpassHz < 40 || p.HighpassHz > 400 || p.PredelayMs < 0 || p.PredelayMs > 200 || p.Mix < 0 || p.Mix > 1 {
		return Error("reverb parameter is invalid or out of range")
	}
	return nil
}

type allpass struct {
	buffer []float64
	index  int
}

func (a *allpass) process(input float64) float64 {
	delayed := a.buffer[a.index]
	output := delayed - .7*input
	a.buffer[a.index] = input + .7*output
	a.index++
	if a.index == len(a.buffer) {
		a.index = 0
	}
	return output
}

type reverbLine struct {
	buffer []float64
	index  int
	low    float64
	high   float64
}

func (l *reverbLine) read(frames float64) float64 {
	position := float64(l.index) - frames
	for position < 0 {
		position += float64(len(l.buffer))
	}
	base := int(position)
	frac := position - float64(base)
	next := base + 1
	if next == len(l.buffer) {
		next = 0
	}
	return l.buffer[base] + (l.buffer[next]-l.buffer[base])*frac
}

var reverbLineLengths = [...]float64{1237, 1381, 1607, 1753, 1871, 2053, 2237, 2411}
var reverbDiffuserLengths = [...]int{142, 107, 379, 277}

// Reverb owns all storage before the render callback starts. SetParams and
// Process do not allocate; length changes fade between stationary read heads.
type Reverb struct {
	sampleRate float64
	params     ReverbParams
	input      [2][4]allpass
	predelay   [2][]float64
	preIndex   int
	lines      [8]reverbLine
	oldLength  [8]float64
	newLength  [8]float64
	lengthFade float64
	oldPre     float64
	newPre     float64
	preFade    float64
	fadeStep   float64
	smooth     float64
	damp       float64
	targetDamp float64
	highpass   float64
	targetHP   float64
	gain       [8]float64
	targetGain [8]float64
	mix        float64
	targetMix  float64
	phase      float64
	phaseStep  float64
	fault      bool
}

func NewReverb(sampleRate int) (*Reverb, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("unsupported reverb sample rate")
	}
	r := &Reverb{
		sampleRate: float64(sampleRate),
		fadeStep:   1 / (.005 * float64(sampleRate)),
		smooth:     1 - math.Exp(-1/(.005*float64(sampleRate))),
		phaseStep:  2 * math.Pi * .3 / float64(sampleRate),
	}
	for channel := range r.input {
		for stage, length := range reverbDiffuserLengths {
			frames := int(math.Round(float64(length) * float64(sampleRate) / 48_000))
			r.input[channel][stage].buffer = make([]float64, frames)
		}
		r.predelay[channel] = make([]float64, sampleRate/5+2)
	}
	for line, base := range reverbLineLengths {
		capacity := int(math.Ceil(base*1.5*float64(sampleRate)/48_000)) + 8
		r.lines[line].buffer = make([]float64, capacity)
	}
	if err := r.SetParams(DefaultReverbParams()); err != nil {
		return nil, err
	}
	r.Reset()
	return r, nil
}

func (r *Reverb) Params() ReverbParams { return r.params }
func (r *Reverb) Fault() bool          { return r.fault }
func (r *Reverb) LatencyFrames() int   { return 0 }

func (r *Reverb) SetParams(p ReverbParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	initial := r.newLength[0] == 0
	lengthFade := r.lengthFade
	changedSize := false
	r.params = p
	for line, base := range reverbLineLengths {
		frames := base * p.Size * r.sampleRate / 48_000
		if initial {
			r.oldLength[line] = frames
		} else if r.newLength[line] != frames {
			r.oldLength[line] = r.oldLength[line]*(1-lengthFade) + r.newLength[line]*lengthFade
			changedSize = true
		}
		r.newLength[line] = frames
		r.targetGain[line] = math.Pow(10, -3*frames/(p.DecaySec*r.sampleRate))
	}
	if initial {
		r.lengthFade = 1
	} else if changedSize {
		r.lengthFade = 0
	}
	pre := p.PredelayMs * r.sampleRate / 1000
	if r.newPre != pre {
		r.oldPre = r.oldPre*(1-r.preFade) + r.newPre*r.preFade
		r.newPre, r.preFade = pre, 0
	}
	r.targetDamp = 1 - math.Exp(-2*math.Pi*p.DampHz/r.sampleRate)
	r.targetHP = math.Exp(-2 * math.Pi * p.HighpassHz / r.sampleRate)
	r.targetMix = p.Mix
	return nil
}

func (r *Reverb) Reset() {
	for channel := range r.input {
		for stage := range r.input[channel] {
			clear(r.input[channel][stage].buffer)
			r.input[channel][stage].index = 0
		}
		clear(r.predelay[channel])
	}
	for line := range r.lines {
		clear(r.lines[line].buffer)
		r.lines[line].index, r.lines[line].low, r.lines[line].high = 0, 0, 0
		r.gain[line] = r.targetGain[line]
		r.oldLength[line] = r.newLength[line]
	}
	r.preIndex, r.phase = 0, 0
	r.oldPre, r.lengthFade, r.preFade = r.newPre, 1, 1
	r.damp, r.highpass, r.mix = r.targetDamp, r.targetHP, r.targetMix
	r.fault = false
}

func (r *Reverb) preRead(channel int, frames float64, input float64) float64 {
	if frames == 0 {
		return input
	}
	buffer := r.predelay[channel]
	if frames < 1 {
		previous := r.preIndex - 1
		if previous < 0 {
			previous = len(buffer) - 1
		}
		return input*(1-frames) + buffer[previous]*frames
	}
	position := float64(r.preIndex) - frames
	if position < 0 {
		position += float64(len(buffer))
	}
	base := int(position)
	frac := position - float64(base)
	next := base + 1
	if next == len(buffer) {
		next = 0
	}
	return buffer[base] + (buffer[next]-buffer[base])*frac
}

func (r *Reverb) Process(left, right float32) (float32, float32) {
	if r.fault {
		return 0, 0
	}
	input := [2]float64{float64(left), float64(right)}
	if !finite(input[0]) || !finite(input[1]) {
		r.fault = true
		return 0, 0
	}
	r.damp += (r.targetDamp - r.damp) * r.smooth
	r.highpass += (r.targetHP - r.highpass) * r.smooth
	r.mix += (r.targetMix - r.mix) * r.smooth
	for channel := range input {
		pre := r.preRead(channel, r.newPre, input[channel])
		if r.preFade < 1 {
			old := r.preRead(channel, r.oldPre, input[channel])
			pre = old*(1-r.preFade) + pre*r.preFade
		}
		r.predelay[channel][r.preIndex] = input[channel]
		for stage := range r.input[channel] {
			pre = r.input[channel][stage].process(pre)
		}
		input[channel] = pre
	}
	r.preIndex++
	if r.preIndex == len(r.predelay[0]) {
		r.preIndex = 0
	}
	if r.preFade < 1 {
		r.preFade = math.Min(1, r.preFade+r.fadeStep)
	}
	modulation := 3 * math.Sin(r.phase)
	r.phase += r.phaseStep
	if r.phase >= 2*math.Pi {
		r.phase -= 2 * math.Pi
	}
	var taps [8]float64
	var sum float64
	for line := range r.lines {
		length := r.newLength[line]
		if line == 1 || line == 4 {
			length += modulation
		}
		value := r.lines[line].read(length)
		if r.lengthFade < 1 {
			oldLength := r.oldLength[line]
			if line == 1 || line == 4 {
				oldLength += modulation
			}
			old := r.lines[line].read(oldLength)
			value = old*(1-r.lengthFade) + value*r.lengthFade
		}
		taps[line] = value
		sum += value
	}
	if r.lengthFade < 1 {
		r.lengthFade = math.Min(1, r.lengthFade+r.fadeStep)
	}
	var wetL, wetR float64
	for line := range r.lines {
		l := &r.lines[line]
		// I - 2vvᵀ for v=(1,...,1)/sqrt(8) is orthogonal.
		feedback := taps[line] - .25*sum
		l.low += (feedback - l.low) * r.damp
		l.high += (l.low - l.high) * (1 - r.highpass)
		high := l.low - l.high
		r.gain[line] += (r.targetGain[line] - r.gain[line]) * r.smooth
		in := input[line&1] * .25
		l.buffer[l.index] = in + high*r.gain[line]
		l.index++
		if l.index == len(l.buffer) {
			l.index = 0
		}
		if line&1 == 0 {
			wetL += taps[line]
		} else {
			wetR += taps[line]
		}
	}
	wetL *= .25
	wetR *= .25
	outL := float64(left)*(1-r.mix) + wetL*r.mix
	outR := float64(right)*(1-r.mix) + wetR*r.mix
	if !finite(outL) || !finite(outR) || math.Abs(outL) > math.MaxFloat32 || math.Abs(outR) > math.MaxFloat32 {
		r.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}
