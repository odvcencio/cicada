// Package tape is a tape-echo delay return. It reuses the delay's design (a
// stereo ring, feedback up to 0.95, a one-pole damping filter, tanh in the
// loop) and replaces the crossfaded read heads with one head whose position
// glides, so a change of time bends the pitch of the repeats like a varispeed
// transport. Wow, flutter and a saturation drive in the feedback loop add the
// tape character. Process does not allocate and does not call the math
// package.
package tape

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/modulate"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrSampleRate Error = "tape echo supports 44100, 48000, and 96000 Hz"
	ErrParams     Error = "tape echo parameter is invalid or out of range"
	ErrTempo      Error = "tape echo tempo must be 20 to 300 BPM"
	ErrTime       Error = "tape echo time must fit 1 ms to the 2-second buffer"
)

const (
	// BufferSeconds is the length of the echo buffer. It bounds the longest time.
	BufferSeconds = 2
	// MaxSlew is the fastest the read head may move, in frames of delay per
	// frame played. 0.5 means the repeats play at 0.5x to 1.5x speed while the
	// time changes, an octave down to a fifth up.
	MaxSlew = 0.5
	// WowDepthMs and FlutterDepthMs are the peak read-head excursions at
	// wow = 1 and flutter = 1.
	WowDepthMs     = 3.0
	FlutterDepthMs = 0.25
)

// Wow and flutter each sum two sine components so the motion does not repeat
// as one obvious tone. The weights add to 1, so the excursion never exceeds the
// stated depth.
var (
	wowHz       = [2]float64{0.37, 1.31} // inside the 0.3 to 2 Hz wow band
	wowWeight   = [2]float64{0.6, 0.4}
	flutterHz   = [2]float64{7.1, 11.3} // inside the 6 to 12 Hz flutter band
	flutterGain = [2]float64{0.6, 0.4}
)

type Params struct {
	Division   fx.DelayDivision // FreeDelay uses TimeMs.
	TimeMs     float64
	Feedback   float64 // 0 to 0.95
	Wow        float64 // 0 to 1
	Flutter    float64 // 0 to 1
	Saturation float64 // 0 to 1
	DampHz     float64 // 1000 to 16000
}

func DefaultParams() Params {
	return Params{Division: fx.Eighth, Feedback: .4, Wow: .4, Flutter: .2, Saturation: .3, DampHz: 5_000}
}

func finite(v float64) bool { return v == v && v <= math.MaxFloat64 && v >= -math.MaxFloat64 }

func unit(v float64) bool { return finite(v) && v >= 0 && v <= 1 }

func (p Params) Validate() error {
	if p.Division.String() == "invalid" || !finite(p.TimeMs) || !finite(p.Feedback) || !finite(p.DampHz) ||
		p.Feedback < 0 || p.Feedback > .95 || p.DampHz < 1_000 || p.DampHz > 16_000 ||
		!unit(p.Wow) || !unit(p.Flutter) || !unit(p.Saturation) {
		return ErrParams
	}
	if p.Division == fx.FreeDelay {
		if p.TimeMs < 1 || p.TimeMs > BufferSeconds*1000 {
			return ErrTime
		}
	} else if p.TimeMs != 0 {
		return ErrParams
	}
	return nil
}

// ValidateTempo catches a synced division that does not fit the buffer.
func (p Params) ValidateTempo(bpmMilli int64) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if bpmMilli < 20_000 || bpmMilli > 300_000 {
		return ErrTempo
	}
	seconds := p.TimeMs / 1000
	if p.Division != fx.FreeDelay {
		seconds = p.Division.Beats() * 60_000 / float64(bpmMilli)
	}
	if seconds < .001 || seconds > BufferSeconds {
		return ErrTime
	}
	return nil
}

type stereo struct{ left, right float64 }

type Tape struct {
	sampleRate, bpmMilli float64
	params               Params
	buffer               []stereo
	position             int
	head, target         float64 // delay in frames; head glides to target
	feedback, tFeedback  float64
	damp, tDamp          float64
	wow, tWow            float64
	flutter, tFlutter    float64
	sat, tSat            float64
	smooth               float64
	wowPhase             [2]uint32
	wowStep              [2]uint32
	flutterPhase         [2]uint32
	flutterStep          [2]uint32
	wowFrames            float64 // frames of excursion at wow = 1
	flutterFrames        float64
	maxDelay             float64
	feedbackState        stereo
	fault, excited       bool
}

func New(sampleRate int, bpmMilli int64) (*Tape, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, ErrSampleRate
	}
	if bpmMilli < 20_000 || bpmMilli > 300_000 {
		return nil, ErrTempo
	}
	r := float64(sampleRate)
	t := &Tape{
		sampleRate: r, bpmMilli: float64(bpmMilli),
		buffer:        make([]stereo, BufferSeconds*sampleRate+8),
		smooth:        1 - math.Exp(-1/(.005*r)),
		wowFrames:     WowDepthMs * r / 1000,
		flutterFrames: FlutterDepthMs * r / 1000,
	}
	t.maxDelay = float64(len(t.buffer) - 3)
	for i := range wowHz {
		t.wowStep[i] = uint32(wowHz[i]*(4294967296/r) + .5)
		t.flutterStep[i] = uint32(flutterHz[i]*(4294967296/r) + .5)
	}
	if err := t.SetParams(DefaultParams()); err != nil {
		return nil, err
	}
	t.Reset()
	return t, nil
}

func (t *Tape) Params() Params     { return t.params }
func (t *Tape) Fault() bool        { return t.fault }
func (t *Tape) LatencyFrames() int { return 0 }

// Silent proves that no audio has entered the echo since Reset. Keep calling
// Process during silence so the head glide and the parameter ramps advance.
func (t *Tape) Silent() bool { return !t.excited && !t.fault }

// Head returns the unmodulated read-head delay in frames, which glides toward
// the target. It is for tests and diagnostics.
func (t *Tape) Head() float64 { return t.head }

// SetTempo moves a synced time to the new tempo by gliding the head. Free
// times stay in milliseconds.
func (t *Tape) SetTempo(bpmMilli int64) error {
	if bpmMilli < 20_000 || bpmMilli > 300_000 {
		return ErrTempo
	}
	if t.params.Division == fx.FreeDelay {
		t.bpmMilli = float64(bpmMilli)
		return nil
	}
	frames, err := t.timeFrames(t.params, float64(bpmMilli))
	if err != nil {
		return err
	}
	t.bpmMilli = float64(bpmMilli)
	t.setTarget(frames)
	return nil
}

func (t *Tape) SetParams(p Params) error {
	if err := p.Validate(); err != nil {
		return err
	}
	frames, err := t.timeFrames(p, t.bpmMilli)
	if err != nil {
		return err
	}
	t.params = p
	t.setTarget(frames)
	t.tFeedback = p.Feedback
	t.tDamp = 1 - math.Exp(-2*math.Pi*p.DampHz/t.sampleRate)
	t.tWow, t.tFlutter, t.tSat = p.Wow, p.Flutter, p.Saturation
	return nil
}

func (t *Tape) timeFrames(p Params, bpmMilli float64) (float64, error) {
	if err := p.ValidateTempo(int64(bpmMilli)); err != nil {
		return 0, err
	}
	var frames float64
	if p.Division == fx.FreeDelay {
		frames = p.TimeMs * t.sampleRate / 1000
	} else {
		frames = p.Division.Beats() * 60_000 * t.sampleRate / bpmMilli
	}
	if frames < t.sampleRate/1000 || frames > BufferSeconds*t.sampleRate {
		return 0, ErrTime
	}
	return frames, nil
}

// setTarget starts the head at its first target. A later target makes the head
// glide there; nothing crossfades.
func (t *Tape) setTarget(frames float64) {
	if t.target == 0 {
		t.head = frames
	}
	t.target = frames
}

func (t *Tape) Reset() {
	clear(t.buffer)
	t.position = 0
	t.feedbackState = stereo{}
	t.fault, t.excited = false, false
	t.head = t.target
	t.feedback, t.damp, t.wow, t.flutter, t.sat = t.tFeedback, t.tDamp, t.tWow, t.tFlutter, t.tSat
	t.wowPhase = [2]uint32{}
	t.flutterPhase = [2]uint32{}
}

func (t *Tape) read(frames float64) stereo {
	position := float64(t.position) - frames
	if position < 0 {
		position += float64(len(t.buffer))
		// A position a hair below zero rounds up to len(buffer) when wrapped;
		// that is the same instant as index zero.
		if position >= float64(len(t.buffer)) {
			position = 0
		}
	}
	base := int(position)
	frac := position - float64(base)
	next := base + 1
	if next == len(t.buffer) {
		next = 0
	}
	a, b := t.buffer[base], t.buffer[next]
	return stereo{a.left + float64((b.left-a.left)*frac), a.right + float64((b.right-a.right)*frac)}
}

// advance moves the smoothed rows, the head glide and the wow and flutter
// phases by one frame, and returns the modulated read delay in frames.
func (t *Tape) advance() float64 {
	t.feedback += float64((t.tFeedback - t.feedback) * t.smooth)
	t.damp += float64((t.tDamp - t.damp) * t.smooth)
	t.wow += float64((t.tWow - t.wow) * t.smooth)
	t.flutter += float64((t.tFlutter - t.flutter) * t.smooth)
	t.sat += float64((t.tSat - t.sat) * t.smooth)
	if step := t.target - t.head; step > MaxSlew {
		t.head += MaxSlew
	} else if step < -MaxSlew {
		t.head -= MaxSlew
	} else {
		t.head = t.target
	}
	delay := t.head
	if t.wow != 0 || t.flutter != 0 {
		var w, f float64
		for i := range t.wowPhase {
			w += float64(wowWeight[i] * float64(modulate.Sin(t.wowPhase[i])) / modulate.One)
			f += float64(flutterGain[i] * float64(modulate.Sin(t.flutterPhase[i])) / modulate.One)
		}
		delay += float64(t.wow*t.wowFrames*w) + float64(t.flutter*t.flutterFrames*f)
	}
	for i := range t.wowPhase {
		t.wowPhase[i] += t.wowStep[i]
		t.flutterPhase[i] += t.flutterStep[i]
	}
	if delay < 1 {
		delay = 1
	} else if delay > t.maxDelay {
		delay = t.maxDelay
	}
	return delay
}

func (t *Tape) Process(left, right float32) (float32, float32) {
	if t.fault {
		return 0, 0
	}
	x := stereo{float64(left), float64(right)}
	if !finite(x.left) || !finite(x.right) {
		t.fault = true
		return 0, 0
	}
	delay := t.advance()
	if left == 0 && right == 0 && !t.excited {
		t.position++
		if t.position == len(t.buffer) {
			t.position = 0
		}
		return 0, 0
	}
	t.excited = true
	wet := t.read(delay)
	t.feedbackState.left += float64((wet.left - t.feedbackState.left) * t.damp)
	t.feedbackState.right += float64((wet.right - t.feedbackState.right) * t.damp)
	// Saturation drives the loop harder and divides the drive back out, so the
	// small-signal loop gain stays at the feedback setting while loud repeats
	// compress. At saturation 0 the drive is 1 and the loop is the delay's.
	drive := 1 + float64(3*t.sat)
	t.buffer[t.position] = stereo{
		x.left + fastmath.Tanh(float64(t.feedbackState.left*t.feedback)*drive)/drive,
		x.right + fastmath.Tanh(float64(t.feedbackState.right*t.feedback)*drive)/drive,
	}
	t.position++
	if t.position == len(t.buffer) {
		t.position = 0
	}
	mid := (wet.left + wet.right) * .5
	side := (wet.left - wet.right) * .5
	outL := mid + side
	outR := mid - side
	if !finite(outL) || !finite(outR) || outL > math.MaxFloat32 || outL < -math.MaxFloat32 || outR > math.MaxFloat32 || outR < -math.MaxFloat32 {
		t.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}
