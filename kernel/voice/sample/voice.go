// Package sample plays caller-owned mono or stereo PCM with bounded voices.
// One audio owner must call its methods. PCM must remain immutable and alive
// until every voice and its release have finished; no asset I/O is performed.
package sample

import "math"

type Error string

func (e Error) Error() string { return string(e) }

// Region uses planar PCM and absolute, exclusive frame bounds. An empty Right
// means mono, duplicated into both outputs. Loop bounds are inside Start:End.
type Region struct {
	Left, Right        []float32
	SampleRate         int
	RootKey            uint8
	Start, End         int
	LoopStart, LoopEnd int
	Loop               bool
}

func (r Region) Validate() error {
	if r.SampleRate < 8000 || r.SampleRate > 192000 || r.RootKey > 127 {
		return Error("sample rate or root key is out of range")
	}
	if r.Start < 0 || r.End <= r.Start || r.End > len(r.Left) || r.End > 1<<30 ||
		(len(r.Right) != 0 && len(r.Right) != len(r.Left)) {
		return Error("sample region bounds or channel lengths are invalid")
	}
	if r.Loop && (r.LoopStart < r.Start || r.LoopEnd > r.End || r.LoopStart >= r.LoopEnd) {
		return Error("sample loop bounds are invalid")
	}
	return nil
}

// Params fixes pitch at NoteOn. Use SetGainTarget for live gain smoothing.
type Params struct {
	Gain, FineTuneCents float64
}

func DefaultParams() Params { return Params{Gain: 1} }

func (p Params) Validate() error {
	if math.IsNaN(p.Gain) || math.IsInf(p.Gain, 0) || p.Gain < 0 || p.Gain > 16 ||
		math.IsNaN(p.FineTuneCents) || math.IsInf(p.FineTuneCents, 0) || math.Abs(p.FineTuneCents) > 9600 {
		return Error("sample gain or fine tune is out of range")
	}
	return nil
}

type Voice struct {
	region                     Region
	params                     Params
	rate, fadeFrames           int
	phase, ratio               float64
	bank                       *sincBank
	velocity, gain, targetGain float64
	gainAlpha                  float64
	active, releasing, looped  bool
	held                       bool
	remaining                  int
	lastL, lastR               float32
	heldL, heldR               float32
	tailL, tailR               float32
	tailRemaining              int
}

func New(sampleRate int, region Region) (*Voice, error) {
	if err := validateRate(sampleRate); err != nil {
		return nil, err
	}
	if err := region.Validate(); err != nil {
		return nil, err
	}
	v := &Voice{}
	v.configure(sampleRate, region)
	return v, nil
}

func validateRate(rate int) error {
	if rate != 44100 && rate != 48000 && rate != 96000 {
		return Error("sample output rate must be 44100, 48000, or 96000")
	}
	return nil
}

func (v *Voice) configure(rate int, region Region) {
	v.rate, v.fadeFrames, v.region, v.params = rate, rate/500, region, DefaultParams()
}

func (v *Voice) SetParams(p Params) error {
	if err := p.Validate(); err != nil {
		return err
	}
	v.params, v.gainAlpha = p, 0
	v.gain = p.Gain * v.velocity
	v.targetGain = v.gain
	return nil
}

// SetGainTarget smooths gain once per output frame, independent of block size.
// alpha is prepared by the caller, in (0,1]. Fine tune affects the next note.
func (v *Voice) SetGainTarget(gain, alpha float64) error {
	if math.IsNaN(gain) || math.IsInf(gain, 0) || gain < 0 || gain > 16 ||
		math.IsNaN(alpha) || math.IsInf(alpha, 0) || alpha <= 0 || alpha > 1 {
		return Error("sample gain smoothing is out of range")
	}
	v.params.Gain, v.targetGain, v.gainAlpha = gain, gain*v.velocity, alpha
	return nil
}

func (v *Voice) playbackRatio(note uint8) (float64, error) {
	if note > 127 {
		return 0, Error("sample note is out of range")
	}
	ratio := float64(v.region.SampleRate) / float64(v.rate)
	if note != v.region.RootKey || v.params.FineTuneCents != 0 {
		ratio *= math.Exp2((float64(note)-float64(v.region.RootKey))/12 + v.params.FineTuneCents/1200)
	}
	if ratio < .125 || ratio > 8 {
		return 0, Error("sample playback ratio must be between 0.125 and 8")
	}
	return ratio, nil
}

// NoteOn restarts at Start. Retriggering retains one held-output declick tail;
// repeated steals replace that tail rather than adding unbounded old voices.
func (v *Voice) NoteOn(note, velocity uint8) error {
	ratio, err := v.playbackRatio(note)
	if err != nil {
		return err
	}
	if velocity > 127 {
		return Error("sample velocity is out of range")
	}
	if v.Active() {
		v.tailL, v.tailR, v.tailRemaining = v.lastL, v.lastR, v.fadeFrames
	}
	v.phase, v.ratio, v.bank = float64(v.region.Start), ratio, bankFor(ratio)
	v.velocity = float64(velocity) / 127
	v.gain, v.targetGain, v.gainAlpha = v.params.Gain*v.velocity, v.params.Gain*v.velocity, 0
	v.active, v.releasing, v.looped, v.held = velocity != 0, false, false, false
	v.looped = v.region.Loop && v.region.Start == v.region.LoopStart
	v.remaining = 0
	return nil
}

// NoteOff linearly releases within floor(sampleRate*0.002) frames. Natural
// one-shot completion fades the last output after the exact source frames.
func (v *Voice) NoteOff() {
	if v.active && !v.releasing {
		v.releasing, v.remaining = true, v.fadeFrames
	}
}

func (v *Voice) Active() bool    { return v.active || v.tailRemaining > 0 }
func (v *Voice) Releasing() bool { return v.releasing || (!v.active && v.tailRemaining > 0) }
func (v *Voice) Ratio() float64  { return v.ratio }
func (v *Voice) KernelTaps() int {
	if v.ratio == 1 || v.bank == nil {
		return 0
	}
	return v.bank.taps
}

func (v *Voice) Reset() {
	v.active, v.releasing, v.looped, v.held = false, false, false, false
	v.phase, v.remaining, v.tailRemaining = 0, 0, 0
	v.lastL, v.lastR, v.tailL, v.tailR, v.heldL, v.heldR = 0, 0, 0, 0, 0, 0
}

// Render overwrites equally sized planar outputs. A mismatch is a caller bug.
func (v *Voice) Render(left, right []float32) {
	if len(left) != len(right) {
		panic("sample output channel lengths differ")
	}
	for i := range left {
		left[i], right[i] = v.NextStereo()
	}
}

func (v *Voice) NextStereo() (float32, float32) {
	var left, right float32
	if v.active {
		if v.gainAlpha != 0 {
			v.gain += float64((v.targetGain - v.gain) * v.gainAlpha)
		}
		gain := v.gain
		if v.releasing {
			gain = float64(gain * (float64(v.remaining) / float64(v.fadeFrames)))
		}
		if v.held {
			// lastL/R already contain gain: hold the last output, not the PCM.
			fade := float64(v.remaining) / float64(v.fadeFrames)
			left, right = float32(float64(v.heldL)*fade), float32(float64(v.heldR)*fade)
		} else if v.ratio == 1 {
			left, right = v.frame(int(v.phase))
			if gain != 1 {
				left, right = float32(float64(left)*gain), float32(float64(right)*gain)
			}
		} else {
			l, r := v.interpolate()
			left, right = float32(l*gain), float32(r*gain)
		}
		if !v.held {
			v.phase += v.ratio
			if v.region.Loop && v.phase >= float64(v.region.LoopEnd) {
				length := float64(v.region.LoopEnd - v.region.LoopStart)
				wraps := int((v.phase - float64(v.region.LoopStart)) / length)
				v.phase -= float64(float64(wraps) * length)
				v.looped = true
			} else if !v.region.Loop && v.phase >= float64(v.region.End) {
				v.held = true
				v.heldL, v.heldR = left, right
				v.NoteOff()
			}
		}
		if v.releasing {
			v.remaining--
			if v.remaining == 0 {
				v.active, v.releasing = false, false
			}
		}
	}
	if v.tailRemaining > 0 {
		fade := float64(v.tailRemaining) / float64(v.fadeFrames)
		left = float32(float64(left) + float64(float64(v.tailL)*fade))
		right = float32(float64(right) + float64(float64(v.tailR)*fade))
		v.tailRemaining--
	}
	v.lastL, v.lastR = left, right
	return left, right
}

// quietness is a conservative envelope estimate, including a retained steal
// tail. It does not depend on the instantaneous zero crossings of the PCM.
func (v *Voice) quietness() float64 {
	level := v.gain
	if !v.active {
		level = 0
	} else if v.releasing {
		level *= float64(v.remaining) / float64(v.fadeFrames)
	}
	if v.tailRemaining > 0 {
		level += max(math.Abs(float64(v.tailL)), math.Abs(float64(v.tailR))) * float64(v.tailRemaining) / float64(v.fadeFrames)
	}
	return level
}
