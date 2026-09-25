package fx

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
)

// DelayDivision is a musical note duration. FreeDelay uses TimeMs instead.
type DelayDivision uint8

const (
	FreeDelay DelayDivision = iota
	ThirtySecond
	Sixteenth
	SixteenthTriplet
	SixteenthDotted
	Eighth
	EighthTriplet
	EighthDotted
	ThreeSixteenths
	Quarter
	QuarterDotted
	Half
	lastDelayDivision
)

var delayDivisionNames = [...]string{
	"free", "1/32", "1/16", "1/16T", "1/16.", "1/8", "1/8T", "1/8.", "3/16", "1/4", "1/4.", "1/2",
}

func ParseDelayDivision(name string) (DelayDivision, error) {
	for division, spelling := range delayDivisionNames {
		if spelling == name {
			return DelayDivision(division), nil
		}
	}
	return 0, Error("unknown delay division")
}

func (division DelayDivision) String() string {
	if division >= lastDelayDivision {
		return "invalid"
	}
	return delayDivisionNames[division]
}

type DelayParams struct {
	Division DelayDivision
	TimeMs   float64
	Feedback float64
	DampHz   float64
	PingPong bool
	Width    float64
	Mix      float64
}

func DefaultDelayParams() DelayParams {
	return DelayParams{Division: Eighth, Feedback: .35, DampHz: 6_000, Width: 1, Mix: 1}
}

func (p DelayParams) Validate() error {
	if p.Division >= lastDelayDivision || !finite(p.TimeMs) || !finite(p.Feedback) || !finite(p.DampHz) || !finite(p.Width) || !finite(p.Mix) ||
		p.Feedback < 0 || p.Feedback > .95 || p.DampHz < 1_000 || p.DampHz > 16_000 || p.Width < 0 || p.Width > 1 || p.Mix < 0 || p.Mix > 1 {
		return Error("delay parameter is invalid or out of range")
	}
	if p.Division == FreeDelay {
		if p.TimeMs < 1 || p.TimeMs > 2_000 {
			return Error("free delay time must be 1 to 2000 ms")
		}
	} else if p.TimeMs != 0 {
		return Error("synced delay time cannot also specify milliseconds")
	}
	return nil
}

// ValidateTempo catches synced divisions that cannot fit the portable
// four-second buffer at a project's current BPM.
func (p DelayParams) ValidateTempo(bpmMilli int64) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if bpmMilli < 20_000 || bpmMilli > 300_000 {
		return Error("delay tempo must be 20 to 300 BPM")
	}
	seconds := p.TimeMs / 1000
	if p.Division != FreeDelay {
		beats := [...]float64{0, .125, .25, 1.0 / 6, .375, .5, 1.0 / 3, .75, .75, 1, 1.5, 2}
		seconds = beats[p.Division] * 60_000 / float64(bpmMilli)
	}
	if seconds < .001 || seconds > 4 {
		return Error("delay time exceeds the 4-second buffer or 1 ms minimum")
	}
	return nil
}

type Delay struct {
	sampleRate, bpmMilli     float64
	params                   DelayParams
	buffer                   []stereo
	position                 int
	oldFrames, newFrames     float64
	fade, fadeStep           float64
	feedback, targetFeedback float64
	damp, targetDamp         float64
	ping, targetPing         float64
	width, targetWidth       float64
	mix, targetMix           float64
	smooth                   float64
	feedbackState            stereo
	fault                    bool
}

func NewDelay(sampleRate int, bpmMilli int64) (*Delay, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 || bpmMilli < 20_000 || bpmMilli > 300_000 {
		return nil, Error("unsupported delay sample rate or tempo")
	}
	d := &Delay{
		sampleRate: float64(sampleRate), bpmMilli: float64(bpmMilli),
		buffer:   make([]stereo, 4*sampleRate+2),
		fadeStep: 1 / (.02 * float64(sampleRate)),
		smooth:   1 - math.Exp(-1/(.005*float64(sampleRate))),
	}
	if err := d.SetParams(DefaultDelayParams()); err != nil {
		return nil, err
	}
	d.Reset()
	return d, nil
}

func (d *Delay) Params() DelayParams { return d.params }
func (d *Delay) Fault() bool         { return d.fault }
func (d *Delay) LatencyFrames() int  { return 0 }

// SetTempo changes a synced time by fading between two stationary read heads.
// Free times stay in milliseconds when the song tempo changes.
func (d *Delay) SetTempo(bpmMilli int64) error {
	if bpmMilli < 20_000 || bpmMilli > 300_000 {
		return Error("delay tempo must be 20 to 300 BPM")
	}
	if d.params.Division == FreeDelay {
		d.bpmMilli = float64(bpmMilli)
		return nil
	}
	frames, err := d.timeFrames(d.params, float64(bpmMilli))
	if err != nil {
		return err
	}
	d.bpmMilli = float64(bpmMilli)
	d.changeTime(frames)
	return nil
}

func (d *Delay) SetParams(p DelayParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	frames, err := d.timeFrames(p, d.bpmMilli)
	if err != nil {
		return err
	}
	d.params = p
	d.changeTime(frames)
	d.targetFeedback = p.Feedback
	d.targetDamp = 1 - math.Exp(-2*math.Pi*p.DampHz/d.sampleRate)
	d.targetWidth, d.targetMix = p.Width, p.Mix
	d.targetPing = 0
	if p.PingPong {
		d.targetPing = 1
	}
	return nil
}

func (d *Delay) timeFrames(p DelayParams, bpmMilli float64) (float64, error) {
	if err := p.ValidateTempo(int64(bpmMilli)); err != nil {
		return 0, err
	}
	var frames float64
	if p.Division == FreeDelay {
		frames = p.TimeMs * d.sampleRate / 1000
	} else {
		beats := [...]float64{0, .125, .25, 1.0 / 6, .375, .5, 1.0 / 3, .75, .75, 1, 1.5, 2}
		frames = beats[p.Division] * 60_000 * d.sampleRate / bpmMilli
	}
	if frames < d.sampleRate/1000 || frames > 4*d.sampleRate {
		return 0, Error("delay time exceeds the 4-second buffer or 1 ms minimum")
	}
	return frames, nil
}

func (d *Delay) changeTime(frames float64) {
	if d.newFrames == 0 {
		d.oldFrames, d.newFrames, d.fade = frames, frames, 1
	} else if frames != d.newFrames {
		d.oldFrames, d.newFrames, d.fade = d.newFrames, frames, 0
	}
}

func (d *Delay) Reset() {
	clear(d.buffer)
	d.position = 0
	d.feedbackState = stereo{}
	d.fault = false
	d.oldFrames, d.fade = d.newFrames, 1
	d.feedback, d.damp, d.ping, d.width, d.mix = d.targetFeedback, d.targetDamp, d.targetPing, d.targetWidth, d.targetMix
}

func (d *Delay) read(frames float64) stereo {
	position := float64(d.position) - frames
	if position < 0 {
		position += float64(len(d.buffer))
	}
	base := int(position)
	frac := position - float64(base)
	next := base + 1
	if next == len(d.buffer) {
		next = 0
	}
	a, b := d.buffer[base], d.buffer[next]
	return stereo{a.left + (b.left-a.left)*frac, a.right + (b.right-a.right)*frac}
}

func (d *Delay) Process(left, right float32) (float32, float32) {
	if d.fault {
		return 0, 0
	}
	x := stereo{float64(left), float64(right)}
	if !finite(x.left) || !finite(x.right) {
		d.fault = true
		return 0, 0
	}
	d.feedback += (d.targetFeedback - d.feedback) * d.smooth
	d.damp += (d.targetDamp - d.damp) * d.smooth
	d.ping += (d.targetPing - d.ping) * d.smooth
	d.width += (d.targetWidth - d.width) * d.smooth
	d.mix += (d.targetMix - d.mix) * d.smooth
	wet := d.read(d.newFrames)
	if d.fade < 1 {
		old := d.read(d.oldFrames)
		wet.left = old.left*(1-d.fade) + wet.left*d.fade
		wet.right = old.right*(1-d.fade) + wet.right*d.fade
		d.fade += d.fadeStep
		if d.fade > 1 {
			d.fade = 1
		}
	}
	feedbackL := wet.left*(1-d.ping) + wet.right*d.ping
	feedbackR := wet.right*(1-d.ping) + wet.left*d.ping
	d.feedbackState.left += (feedbackL - d.feedbackState.left) * d.damp
	d.feedbackState.right += (feedbackR - d.feedbackState.right) * d.damp
	d.buffer[d.position] = stereo{
		x.left + fastmath.Tanh(d.feedbackState.left*d.feedback),
		x.right + fastmath.Tanh(d.feedbackState.right*d.feedback),
	}
	d.position++
	if d.position == len(d.buffer) {
		d.position = 0
	}
	mid := (wet.left + wet.right) * .5
	side := (wet.left - wet.right) * .5 * d.width
	outL := x.left*(1-d.mix) + (mid+side)*d.mix
	outR := x.right*(1-d.mix) + (mid-side)*d.mix
	if !finite(outL) || !finite(outR) || math.Abs(outL) > math.MaxFloat32 || math.Abs(outR) > math.MaxFloat32 {
		d.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}
