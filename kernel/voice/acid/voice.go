// Package acid implements Cicada's built-in monophonic acid voice. It is
// separate from the user-authored graph executor and has its own DSP state.
package acid

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/dsp/halfband"
)

type FilterModel uint8

const (
	Diode FilterModel = iota
	Ladder
)

type Error string

func (e Error) Error() string { return string(e) }

type Params struct {
	Tune, Fine, Wave, PulseWidth, Detune, Sub float64
	Cutoff, Resonance, EnvMod, Decay, Accent  float64
	Drive, Release, Slide, Gate, LevelDB      float64
	Filter                                    FilterModel
	Savage                                    bool
}

func DefaultParams() Params {
	return Params{
		PulseWidth: .5, Cutoff: 600, Resonance: .55, EnvMod: .6,
		Decay: .4, Accent: .7, Drive: .2, Release: .03, Slide: .06,
		Gate: 55, LevelDB: -6, Filter: Diode,
	}
}

func (p Params) Validate() error {
	values := [...]float64{p.Tune, p.Fine, p.Wave, p.PulseWidth, p.Detune, p.Sub, p.Cutoff, p.Resonance, p.EnvMod, p.Decay, p.Accent, p.Drive, p.Release, p.Slide, p.Gate, p.LevelDB}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Error("acid parameter must be finite")
		}
	}
	if p.Tune < -12 || p.Tune > 12 || p.Fine < -100 || p.Fine > 100 || p.Wave < 0 || p.Wave > 1 || p.PulseWidth < .1 || p.PulseWidth > .9 || p.Detune < 0 || p.Detune > 50 || p.Sub < 0 || p.Sub > 1 {
		return Error("acid oscillator parameter is out of range")
	}
	if p.Cutoff < 20 || p.Cutoff > 8000 || p.Resonance < 0 || p.Resonance > 1 || p.EnvMod < 0 || p.EnvMod > 1 || p.Decay < .1 || p.Decay > 3 || p.Accent < 0 || p.Accent > 1 || p.Drive < 0 || p.Drive > 1 {
		return Error("acid filter or envelope parameter is out of range")
	}
	if p.Release < .005 || p.Release > .5 || p.Slide < .02 || p.Slide > .2 || p.Gate < 10 || p.Gate > 100 || p.LevelDB > 6 || p.Filter > Ladder {
		return Error("acid voice parameter is out of range")
	}
	return nil
}

type filterState struct {
	state [4]float64
	last  float64
}

type Voice struct {
	sampleRate                                  float64
	params                                      Params
	phaseA, phaseB, phaseSub                    float64
	pitchLog, targetLog                         float64
	lastPitchLog, pitchDelta, detuneRatio       float64
	pitchCached                                 bool
	gate, attacking, accented                   bool
	meg, vca, cap, sweepStrength                float64
	accentGain, accentTarget                    float64
	megDecay, capDecay, releaseDecay, holdDecay float64
	pitchAlpha, accentAlpha, switchAlpha        float64
	filterBlend, savageBlend                    float64
	drivePre, drivePost, level                  float64
	driveTable                                  [33]float64
	up, down                                    halfband.FIR
	diode, ladder                               filterState
	fault                                       bool
}

func New(sampleRate int) (*Voice, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("acid sample rate must be 44100, 48000, or 96000")
	}
	v := &Voice{sampleRate: float64(sampleRate), up: halfband.New(), down: halfband.New()}
	v.buildDriveTable()
	if err := v.SetParams(DefaultParams()); err != nil {
		return nil, err
	}
	v.Reset()
	return v, nil
}

func (v *Voice) Params() Params          { return v.params }
func (v *Voice) Fault() bool             { return v.fault }
func (v *Voice) AccentStrength() float64 { return v.sweepStrength }

func (v *Voice) SetParams(p Params) error {
	if err := p.Validate(); err != nil {
		return err
	}
	v.params = p
	v.capDecay = math.Exp(-1 / ((.08 + .17*p.Resonance) * v.sampleRate))
	v.releaseDecay = math.Exp(-4.6 / (p.Release * v.sampleRate))
	v.holdDecay = math.Exp(-1 / (3 * v.sampleRate))
	v.pitchAlpha = 1 - math.Exp(-1/(p.Slide*v.sampleRate))
	v.accentAlpha = 1 - math.Exp(-1/(.0015*v.sampleRate))
	v.switchAlpha = 1 - math.Exp(-1/(.02*v.sampleRate))
	v.drivePre = math.Pow(10, p.Drive*36/20)
	v.detuneRatio = fastmath.Exp2(p.Detune / 1200)
	table := p.Drive * 32
	index := int(table)
	if index >= 32 {
		index = 31
		table = 32
	}
	frac := table - float64(index)
	v.drivePost = v.driveTable[index]*(1-frac) + v.driveTable[index+1]*frac
	v.level = math.Pow(10, p.LevelDB/20)
	return nil
}

func (v *Voice) buildDriveTable() {
	const inputAmplitude = 0.251188643150958
	inputRMS := inputAmplitude / math.Sqrt2
	for i := range v.driveTable {
		pre := math.Pow(10, float64(i)*36/(32*20))
		var outputPower float64
		for sample := 0; sample < 1024; sample++ {
			x := inputAmplitude * math.Sin(2*math.Pi*float64(sample)/1024)
			y := fastmath.Tanh(pre * x)
			outputPower += y * y
		}
		v.driveTable[i] = inputRMS / math.Sqrt(outputPower/1024)
	}
}

func (v *Voice) NoteOn(note uint8, accent, slide bool, velocity uint8) {
	if note > 127 {
		note = 127
	}
	target := math.Log2(440) + (float64(note)-69+v.params.Tune+v.params.Fine/100)/12
	if slide && v.gate {
		v.targetLog = target
		return
	}
	v.pitchLog, v.targetLog = target, target
	v.phaseA, v.phaseB, v.phaseSub = 0, .37, 0
	v.gate, v.attacking, v.accented = true, true, accent
	v.meg, v.vca = 1, 0
	v.megDecay = math.Exp(-4.6 / (v.params.Decay * v.sampleRate))
	if accent {
		v.megDecay = math.Exp(-4.6 / (.2 * v.sampleRate))
		v.sweepStrength = 1 + v.cap
		v.cap = min(.6, v.cap+.3)
		v.accentTarget = 1 + v.params.Accent
	} else {
		v.sweepStrength = 0
		v.accentTarget = 1
	}
	_ = velocity // acid ignores MIDI velocity; accent controls loudness.
}

func (v *Voice) NoteOff() { v.gate = false }

func (v *Voice) Active() bool { return v.gate || v.vca > 1e-5 }

func (v *Voice) Reset() {
	v.phaseA, v.phaseB, v.phaseSub = 0, 0, 0
	v.pitchLog, v.targetLog = 0, 0
	v.lastPitchLog, v.pitchDelta, v.pitchCached = 0, 0, false
	v.gate, v.attacking, v.accented, v.fault = false, false, false, false
	v.meg, v.vca, v.cap, v.sweepStrength = 0, 0, 0, 0
	v.accentGain, v.accentTarget = 1, 1
	v.filterBlend, v.savageBlend = 0, 0
	v.diode, v.ladder = filterState{}, filterState{}
	v.up.Reset()
	v.down.Reset()
}

func (v *Voice) Render(output []float32) {
	for i := range output {
		output[i] = v.Next()
	}
}

func (v *Voice) Next() float32 {
	if v.fault {
		return 0
	}
	// Stop decays far below audibility before denormals slow the audio thread.
	const envelopeFloor = 1e-18
	v.cap *= v.capDecay
	if v.cap < envelopeFloor {
		v.cap = 0
	}
	v.meg *= v.megDecay
	if v.meg < envelopeFloor {
		v.meg = 0
	}
	v.pitchLog += (v.targetLog - v.pitchLog) * v.pitchAlpha
	v.accentGain += (v.accentTarget - v.accentGain) * v.accentAlpha
	filterTarget := 0.0
	if v.params.Filter == Ladder {
		filterTarget = 1
	}
	v.filterBlend += (filterTarget - v.filterBlend) * v.switchAlpha
	if v.filterBlend < 1e-6 {
		v.filterBlend = 0
	} else if v.filterBlend > 1-1e-6 {
		v.filterBlend = 1
	}
	savageTarget := 0.0
	if v.params.Savage {
		savageTarget = 1
	}
	v.savageBlend += (savageTarget - v.savageBlend) * v.switchAlpha
	if v.gate {
		if v.attacking {
			v.vca += 1 / (.003 * v.sampleRate)
			if v.vca >= 1 {
				v.vca = 1
				v.attacking = false
			}
		} else {
			v.vca *= v.holdDecay
		}
	} else {
		v.vca *= v.releaseDecay
	}
	if v.vca < envelopeFloor {
		v.vca = 0
	}
	if !v.pitchCached || v.pitchLog != v.lastPitchLog {
		v.pitchDelta = min(fastmath.Exp2(v.pitchLog)/v.sampleRate, .49)
		v.lastPitchLog = v.pitchLog
		v.pitchCached = true
	}
	delta := v.pitchDelta
	saw := 2*v.phaseA - 1 - polyBLEP(v.phaseA, delta)
	square := pulse(v.phaseA, delta, v.params.PulseWidth)
	osc := saw*(1-v.params.Wave) + square*v.params.Wave
	v.phaseA = fraction(v.phaseA + delta)
	if v.params.Detune > 0 {
		secondDelta := min(delta*v.detuneRatio, .49)
		second := pulse(v.phaseB, secondDelta, v.params.PulseWidth)
		osc += .5 * second
		v.phaseB = fraction(v.phaseB + secondDelta)
	}
	if v.params.Sub > 0 {
		subDelta := delta * .5
		osc += v.params.Sub * .5 * pulse(v.phaseSub, subDelta, .5)
		v.phaseSub = fraction(v.phaseSub + subDelta)
	}
	pre := fastmath.Tanh(v.drivePre*osc) * v.drivePost
	first, second := v.up.Upsample(pre)
	cutoff := v.cutoffHz()
	g := fastmath.TanSmall(math.Pi * cutoff / (2 * v.sampleRate))
	first = v.filter(first, g)
	second = v.filter(second, g)
	y := v.down.Downsample(first, second)
	y *= v.vca * v.accentGain * v.level
	if v.savageBlend > 0 {
		aggressive := fastmath.Tanh(2*y) * .5
		y = y*(1-v.savageBlend) + aggressive*v.savageBlend
	}
	if math.IsNaN(y) || math.IsInf(y, 0) {
		v.fault = true
		v.gate = false
		return 0
	}
	if y > 4 {
		y = 4
	} else if y < -4 {
		y = -4
	}
	return float32(y)
}

func (v *Voice) cutoffHz() float64 {
	cv := v.params.EnvMod*5*v.meg + v.sweepStrength*v.meg*2.5*v.params.Accent
	return max(20, min(v.params.Cutoff*fastmath.Exp2(cv), .45*v.sampleRate))
}

func (v *Voice) filter(input, baseG float64) float64 {
	kDiode := 17 * v.params.Resonance * (1 + .25*v.savageBlend)
	kLadder := 4 * v.params.Resonance * (1 + .25*v.savageBlend)
	if v.filterBlend == 0 {
		g := calibratedFilterG(baseG, Diode)
		return v.diode.processDiode(input, g/(1+g), g, kDiode, v.savageBlend) * (1 + .35*v.params.Resonance*(1-v.savageBlend))
	}
	if v.filterBlend == 1 {
		g := calibratedFilterG(baseG, Ladder)
		return v.ladder.processLadder(input, g/(1+g), g, kLadder, v.savageBlend) * (1 + .5*kLadder*(1-v.savageBlend))
	}
	gDiode := calibratedFilterG(baseG, Diode)
	gLadder := calibratedFilterG(baseG, Ladder)
	diode := v.diode.processDiode(input, gDiode/(1+gDiode), gDiode, kDiode, v.savageBlend)
	ladder := v.ladder.processLadder(input, gLadder/(1+gLadder), gLadder, kLadder, v.savageBlend)
	diode *= 1 + .35*v.params.Resonance*(1-v.savageBlend)
	ladder *= 1 + .5*kLadder*(1-v.savageBlend)
	return diode*(1-v.filterBlend) + ladder*v.filterBlend
}

const ladderCutoffRatio = 0.43497944204608224
const diodeCutoffRatio = 0.07467815580279147

// At zero resonance, the ladder's four cascaded TPT poles reach -3 dB when
// tan(pi*f/fso)/g is sqrt(2^(1/4)-1). Solving the diode's coupled four-stage
// transfer function gives its ratio above. Applying these maps to the user's
// cutoff keeps the measured -3 dB point at that frequency across rates.
func calibratedFilterG(baseG float64, model FilterModel) float64 {
	if model == Ladder {
		return baseG / ladderCutoffRatio
	}
	return baseG / diodeCutoffRatio
}

func fraction(value float64) float64 {
	if value >= 1 {
		return value - 1
	}
	return value
}

func polyBLEP(phase, delta float64) float64 {
	if phase < delta {
		t := phase / delta
		return 2*t - t*t - 1
	}
	if phase > 1-delta {
		t := (phase - 1) / delta
		return t*t + 2*t + 1
	}
	return 0
}

func pulse(phase, delta, width float64) float64 {
	value := -1.0
	if phase < width {
		value = 1
	}
	return value + polyBLEP(phase, delta) - polyBLEP(fraction(phase+1-width), delta)
}
