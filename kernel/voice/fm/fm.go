// Package fm implements an original six-operator phase-modulation keyboard.
// One audio owner must serialize its note, pedal and render calls.
package fm

import "math"

const (
	MinNote   = 21
	MaxNote   = 108
	MaxVoices = 8
	Operators = 6
	keyCount  = MaxNote - MinNote + 1
	tableBits = 11
	tableSize = 1 << tableBits
	firSize   = 128
)

type Error string

func (e Error) Error() string { return string(e) }

// Envelope times are seconds. Decay and release reach -60 dB of their starting
// distance after their specified time. Sustain is a fraction of peak level.
type Envelope struct {
	Attack, Decay, Sustain, Release float32
}

// Operator describes one sine operator. Level is linear amplitude for a
// carrier and radians of phase modulation for a modulator. Ratio multiplies
// equal-tempered pitch; Detune is cents. Velocity controls level sensitivity
// from zero (constant level) to one (quadratic velocity). KeyTracking shortens
// decay/release toward the treble. Pan applies only to audible output.
type Operator struct {
	Ratio, Detune, Level, Velocity, KeyTracking, Pan, Feedback float32
	Envelope                                                   Envelope
}

// Params describes a directed six-operator network. Routing[destination][source]
// must be zero unless source > destination; Feedback closes each operator's
// own two-sample loop. Output selects audible carriers. Gain is overall level,
// and StereoSpread scales operator pan and a gentle keyboard position spread.
type Params struct {
	Operators          [Operators]Operator
	Routing            [Operators][Operators]float32
	Output             [Operators]float32
	Gain, StereoSpread float32
}

// DefaultParams returns the original fm_ep patch.
func DefaultParams() Params {
	p, _ := Patch("fm_ep")
	return p
}

// Patch returns an original patch. No factory patch data is used.
func Patch(name string) (Params, error) {
	p := Params{Gain: .3, StereoSpread: .7}
	op := func(ratio, level, velocity, attack, decay, sustain, release float32) Operator {
		return Operator{Ratio: ratio, Level: level, Velocity: velocity, KeyTracking: .5,
			Envelope: Envelope{Attack: attack, Decay: decay, Sustain: sustain, Release: release}}
	}
	switch name {
	case "fm_ep":
		p.Operators = [Operators]Operator{
			op(1, 1, .7, .0025, 4.2, .16, .28),
			op(1, 1.5, .95, .0012, 1.1, .12, .2),
			op(1, .28, .85, .0015, 1.5, .04, .22),
			op(14, 1.15, 1, .0007, .24, 0, .12),
			op(1, .38, .65, .003, 3.3, .1, .3),
			op(1, .8, .8, .002, 1.8, .09, .2),
		}
		p.Operators[2].Detune, p.Operators[4].Detune = 1.1, -1.1
		p.Operators[2].Pan, p.Operators[4].Pan = -.7, .7
		p.Operators[5].Feedback = .12
		p.Routing[0][1], p.Routing[2][3], p.Routing[4][5] = 1, 1, 1
		p.Output[0], p.Output[2], p.Output[4] = .7, .7, .7
	case "bell_keys":
		p.Gain = .27
		p.Operators = [Operators]Operator{
			op(1, 1, .65, .001, 7, 0, 1.1),
			op(2.756, 1.9, 1, .0008, 2.3, 0, .7),
			op(2, .3, .75, .0012, 3.4, 0, .8),
			op(5.405, 1.1, .95, .0006, .85, 0, .3),
			op(3.01, .19, .7, .0008, 2.8, 0, .65),
			op(1, .5, .9, .001, .42, 0, .24),
		}
		p.Operators[2].Pan, p.Operators[4].Pan = -.65, .65
		p.Routing[0][1], p.Routing[2][3], p.Routing[4][5] = 1, 1, 1
		p.Output[0], p.Output[2], p.Output[4] = .8, .8, .8
	case "fm_bass":
		p.Gain, p.StereoSpread = .35, .15
		p.Operators = [Operators]Operator{
			op(1, 1, .65, .002, 1.4, .3, .12),
			op(.5, .6, .7, .003, 1.8, .25, .15),
			op(1, 1.5, .95, .001, .8, .1, .1),
			op(2, .55, .85, .001, .3, .03, .06),
			op(.5, .6, .85, .002, .9, .07, .12),
			op(1.5, .4, 1, .001, .2, 0, .08),
		}
		p.Operators[2].Feedback = .2
		p.Routing[0][2], p.Routing[2][3], p.Routing[1][4], p.Routing[4][5] = 1, 1, 1, 1
		p.Output[0], p.Output[1] = .7, .7
	default:
		return Params{}, Error("unknown FM patch: use fm_ep, bell_keys, or fm_bass")
	}
	return p, nil
}

type preparedOperator struct {
	phaseStep                                                      uint32
	attackStep, decay, sustain, release, level, velocity, feedback float32
	attackFrames, releaseFrames                                    int
	route                                                          [Operators]float32
	left, right                                                    float32
}

type key struct{ ops [Operators]preparedOperator }
type envelopeState struct {
	value  float32
	frames int
	stage  uint8
}
type operatorState struct {
	phase                   uint32
	envelope                envelopeState
	level, output, previous float32
}
type voice struct {
	key          *key
	ops          [Operators]operatorState
	note         uint8
	held         bool
	age          uint64
	lastL, lastR float32
}

// Instrument owns eight fixed voice slots, all prepared key coefficients,
// a sine table and a shared two-times oversampling decimator. New allocates;
// triggering, sustain and rendering allocate nothing and design no coefficients.
type Instrument struct {
	rate                    int
	voiceLimit              int
	keys                    [keyCount]key
	voices                  [MaxVoices]voice
	sine                    [tableSize + 1]float32
	fir                     [firSize / 2]float32
	historyL, historyR      [firSize]float32
	historyIndex            int
	sustain, gain           float32
	serial                  uint64
	tailL, tailR, tailDecay float32
	tailFrames, stealFrames int
}

// SetVoiceLimit selects one to eight note slots. Configure it before playback;
// reducing it discards slots above the new limit. Reset preserves the limit.
// This method allocates nothing.
func (f *Instrument) SetVoiceLimit(limit int) error {
	if limit < 1 || limit > MaxVoices {
		return Error("FM voice limit must be one to eight")
	}
	for i := limit; i < MaxVoices; i++ {
		f.voices[i] = voice{}
	}
	f.voiceLimit = limit
	return nil
}

// Reset immediately clears voices, sustain, decimator history and steal tails.
// Prepared coefficients and the patch remain unchanged. Reset allocates nothing.
func (f *Instrument) Reset() {
	f.voices = [MaxVoices]voice{}
	f.historyL, f.historyR = [firSize]float32{}, [firSize]float32{}
	f.historyIndex, f.tailFrames = 0, 0
	f.sustain, f.tailL, f.tailR, f.serial = 0, 0, 0, 0
}

func bounded(x, low, high float32) bool { return x >= low && x <= high }

func validate(p Params) error {
	if !bounded(p.Gain, .001, 2) || !bounded(p.StereoSpread, 0, 1) {
		return Error("FM gain or stereo spread is out of range")
	}
	var output float32
	for i, o := range p.Operators {
		e := o.Envelope
		if !bounded(o.Ratio, .125, 32) || !bounded(o.Detune, -50, 50) || !bounded(o.Level, 0, 8) ||
			!bounded(o.Velocity, 0, 1) || !bounded(o.KeyTracking, 0, 1) || !bounded(o.Pan, -1, 1) ||
			!bounded(o.Feedback, 0, 2) || !bounded(e.Attack, 0, 5) || !bounded(e.Decay, 0, 30) ||
			!bounded(e.Sustain, 0, 1) || !bounded(e.Release, .005, 30) || !bounded(p.Output[i], 0, 1) {
			return Error("FM operator parameter is out of range")
		}
		output += float32(p.Output[i] * o.Level)
		for j, amount := range p.Routing[i] {
			if !bounded(amount, 0, 4) || j <= i && amount != 0 {
				return Error("FM routing must feed only lower numbered operators")
			}
		}
	}
	if output <= 0 {
		return Error("FM patch must have an audible carrier")
	}
	return nil
}

func New(sampleRate int, params Params) (*Instrument, error) {
	if sampleRate != 44100 && sampleRate != 48000 && sampleRate != 96000 && sampleRate != 192000 {
		return nil, Error("FM sample rate must be 44100, 48000, 96000, or 192000")
	}
	if err := validate(params); err != nil {
		return nil, err
	}
	f := &Instrument{rate: sampleRate, voiceLimit: MaxVoices, gain: params.Gain, stealFrames: sampleRate / 100}
	f.tailDecay = float32(math.Exp(-8 / float64(f.stealFrames)))
	for i := range f.sine {
		f.sine[i] = float32(math.Sin(2 * math.Pi * float64(i) / tableSize))
	}
	f.sine[tableSize] = 0
	// A 128-tap Blackman-windowed sinc is shared by the whole instrument.
	// Its pass band ends below Nyquist; symmetry halves the multiply count.
	var sum float64
	for i := range f.fir {
		x := float64(i) - float64(firSize-1)/2
		window := .42 - .5*math.Cos(2*math.Pi*float64(i)/float64(firSize-1)) + .08*math.Cos(4*math.Pi*float64(i)/float64(firSize-1))
		coefficient := math.Sin(2*math.Pi*.2*x) / (math.Pi * x) * window
		f.fir[i] = float32(coefficient)
		sum += 2 * coefficient
	}
	for i := range f.fir {
		f.fir[i] = float32(float64(f.fir[i]) / sum)
	}
	for i := range f.keys {
		f.prepareKey(i, params)
	}
	return f, nil
}

func (f *Instrument) prepareKey(index int, p Params) {
	note := index + MinNote
	frequency := 440 * math.Exp2(float64(note-69)/12)
	internalRate := float64(f.rate * 2)
	guard := float64(f.rate) * .39
	var bandwidth [Operators]float64
	for i := Operators - 1; i >= 0; i-- {
		o, k := p.Operators[i], &f.keys[index].ops[i]
		hz := frequency * float64(o.Ratio) * math.Exp2(float64(o.Detune)/1200)
		k.level = o.Level
		if hz >= guard {
			k.level = 0
		}
		// An inaudible high-ratio operator never wraps into an audible pitch.
		k.phaseStep = uint32(min(hz, guard) / internalRate * 4294967296.)
		var deviation float64
		for j := i + 1; j < Operators; j++ {
			deviation += float64(p.Routing[i][j]) * float64(f.keys[index].ops[j].level) * bandwidth[j]
		}
		deviation += float64(o.Feedback) * float64(k.level) * hz
		// Carson-style significant-sideband guard. The 0.8 reserve leaves
		// room for weak higher-order sidebands before the decimator stop band.
		scale := 1.
		if deviation > 0 {
			scale = min(1., max(0., (guard-hz)*.8/deviation))
		}
		for j := i + 1; j < Operators; j++ {
			k.route[j] = float32(float64(p.Routing[i][j]) * scale)
		}
		k.feedback = float32(float64(o.Feedback) * scale)
		bandwidth[i] = min(guard, hz+deviation*scale)
		if k.level == 0 {
			bandwidth[i] = 0
		}
		timeScale := math.Exp2(float64(60-note) * float64(o.KeyTracking) / 24)
		e := o.Envelope
		k.attackFrames = int(math.Ceil(float64(e.Attack) * internalRate))
		if k.attackFrames > 0 {
			k.attackStep = 1 / float32(k.attackFrames)
		}
		if e.Decay > 0 {
			k.decay = float32(math.Exp(-6.907755278982137 / (float64(e.Decay) * timeScale * internalRate)))
		}
		k.releaseFrames = int(math.Ceil(float64(e.Release) * timeScale * internalRate))
		k.release = float32(math.Exp(-6.907755278982137 / float64(k.releaseFrames)))
		k.sustain, k.velocity = e.Sustain, o.Velocity
		pan := float32(float32(float32(note-64)/44)*.2) + o.Pan
		pan = float32(min(float32(1), max(float32(-1), pan)) * p.StereoSpread)
		level := float32(p.Output[i] * .5)
		k.left, k.right = float32(level*(1-pan)), float32(level*(1+pan))
	}
}

func (f *Instrument) NoteOn(note, velocity uint8) error {
	if note < MinNote || note > MaxNote || velocity > 127 {
		return Error("FM note or velocity is out of range")
	}
	if velocity == 0 {
		f.NoteOff(note)
		return nil
	}
	index := -1
	for i := 0; i < f.voiceLimit; i++ {
		if f.voices[i].key != nil && f.voices[i].note == note {
			index = i
			break
		}
	}
	if index < 0 {
		for i := 0; i < f.voiceLimit; i++ {
			if f.voices[i].key == nil {
				index = i
				break
			}
		}
	}
	if index < 0 {
		index = 0
		for i := 1; i < f.voiceLimit; i++ {
			v, best := &f.voices[i], &f.voices[index]
			if !v.held && best.held || v.held == best.held && v.age < best.age {
				index = i
			}
		}
	}
	v := &f.voices[index]
	if v.key != nil {
		f.tailL += v.lastL
		f.tailR += v.lastR
		f.tailFrames = f.stealFrames
	}
	*v = voice{key: &f.keys[note-MinNote], note: note, held: true, age: f.serial}
	f.serial++
	velocityLevel := float32(velocity) / 127
	velocityLevel = float32(velocityLevel * velocityLevel)
	for i := range v.ops {
		k, o := &v.key.ops[i], &v.ops[i]
		o.level = float32(k.level * float32((1-k.velocity)+float32(k.velocity*velocityLevel)))
		if k.attackFrames == 0 {
			o.envelope = envelopeState{value: 1, stage: 1}
		}
	}
	return nil
}

func releaseVoice(v *voice) {
	for i := range v.ops {
		v.ops[i].envelope.stage, v.ops[i].envelope.frames = 3, 0
	}
}

func (f *Instrument) NoteOff(note uint8) {
	for i := range f.voices {
		v := &f.voices[i]
		if v.key != nil && v.note == note && v.held {
			v.held = false
			if f.sustain < .5 {
				releaseVoice(v)
			}
		}
	}
}

// AllNotesOff releases all keys. A held sustain pedal retains their envelopes.
func (f *Instrument) AllNotesOff() {
	for i := range f.voices {
		v := &f.voices[i]
		if v.key != nil && v.held {
			v.held = false
			if f.sustain < .5 {
				releaseVoice(v)
			}
		}
	}
}

// SetSustain uses MIDI-style pedal semantics: values >= 0.5 hold released keys.
func (f *Instrument) SetSustain(value float32) error {
	if !bounded(value, 0, 1) {
		return Error("FM sustain must be between zero and one")
	}
	if f.sustain >= .5 && value < .5 {
		for i := range f.voices {
			if f.voices[i].key != nil && !f.voices[i].held {
				releaseVoice(&f.voices[i])
			}
		}
	}
	f.sustain = value
	return nil
}

func nextEnvelope(e *envelopeState, k *preparedOperator) float32 {
	switch e.stage {
	case 0:
		e.frames++
		e.value += k.attackStep
		if e.frames >= k.attackFrames {
			e.value, e.stage, e.frames = 1, 1, 0
		}
	case 1:
		e.value = k.sustain + float32((e.value-k.sustain)*k.decay)
		if e.value-k.sustain < .00001 {
			e.value, e.stage = k.sustain, 2
		}
	case 3:
		e.frames++
		e.value = float32(e.value * k.release)
		if e.frames >= k.releaseFrames {
			e.value, e.stage = 0, 4
		}
	}
	return e.value
}

func (f *Instrument) lookup(phase uint32) float32 {
	index := phase >> (32 - tableBits)
	fraction := float32(phase&((1<<(32-tableBits))-1)) * (1. / (1 << (32 - tableBits)))
	a, b := f.sine[index], f.sine[index+1]
	return a + float32((b-a)*fraction)
}

func (f *Instrument) nextInternal() (float32, float32) {
	var left, right float32
	for vIndex := 0; vIndex < f.voiceLimit; vIndex++ {
		v := &f.voices[vIndex]
		if v.key == nil {
			continue
		}
		var l, r float32
		alive := false
		for i := Operators - 1; i >= 0; i-- {
			o, k := &v.ops[i], &v.key.ops[i]
			envelope := nextEnvelope(&o.envelope, k)
			var modulation float32
			for j := i + 1; j < Operators; j++ {
				modulation += float32(v.ops[j].output * k.route[j])
			}
			feedback := float32(float32(o.output+o.previous) * .5)
			modulation += float32(feedback * k.feedback)
			// Integer phase wrapping and explicit float32 products give fixed
			// rounding boundaries on native Go and TinyGo's WASM backend.
			phase := o.phase + uint32(int64(float32(modulation*683565275.578)))
			value := float32(float32(f.lookup(phase)*envelope) * o.level)
			o.previous, o.output = o.output, value
			o.phase += k.phaseStep
			l += float32(value * k.left)
			r += float32(value * k.right)
			if k.left+k.right > 0 && envelope > 0 {
				alive = true
			}
		}
		v.lastL, v.lastR = l, r
		left += l
		right += r
		if !alive {
			v.key = nil
		}
	}
	if f.tailFrames > 0 {
		left += f.tailL
		right += f.tailR
		f.tailL, f.tailR = float32(f.tailL*f.tailDecay), float32(f.tailR*f.tailDecay)
		f.tailFrames--
		if f.tailFrames == 0 {
			f.tailL, f.tailR = 0, 0
		}
	}
	return left, right
}

// NextStereo renders one output frame. The network runs at twice the requested
// rate; the shared FIR removes ultrasonic sidebands before downsampling.
func (f *Instrument) NextStereo() (float32, float32) {
	for range 2 {
		left, right := f.nextInternal()
		f.historyL[f.historyIndex], f.historyR[f.historyIndex] = left, right
		f.historyIndex = (f.historyIndex + 1) & (firSize - 1)
	}
	var left, right float32
	for i, coefficient := range f.fir {
		a, b := (f.historyIndex+i)&(firSize-1), (f.historyIndex+firSize-1-i)&(firSize-1)
		left += float32(float32(f.historyL[a]+f.historyL[b]) * coefficient)
		right += float32(float32(f.historyR[a]+f.historyR[b]) * coefficient)
	}
	return float32(left * f.gain), float32(right * f.gain)
}
