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

type restingDiodeShape struct {
	G, k, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen, gain float64
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
	filterBlend, savageBlend, restingFilterG    float64
	restingDiode                                restingDiodeShape
	drivePre, drivePost, level                  float64
	driveTable                                  [33]float64
	up, down                                    halfband.FIR
	oscillator                                  *oscillatorBank
	oscA, oscB, oscSub                          oscillatorSelection
	diode, ladder                               filterState
	fault                                       bool
}

func New(sampleRate int) (*Voice, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("acid sample rate must be 44100, 48000, or 96000")
	}
	v := &Voice{sampleRate: float64(sampleRate), up: halfband.New(), down: halfband.New(), oscillator: bankForSampleRate(sampleRate)}
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
	v.pitchCached = false
	table := p.Drive * 32
	index := int(table)
	if index >= 32 {
		index = 31
		table = 32
	}
	frac := table - float64(index)
	v.drivePost = v.driveTable[index]*(1-frac) + v.driveTable[index+1]*frac
	v.level = math.Pow(10, p.LevelDB/20)
	v.restingFilterG = fastmath.TanSmall(math.Pi * p.Cutoff / (2 * v.sampleRate))
	v.prepareRestingDiode()
	return nil
}

func (v *Voice) prepareRestingDiode() {
	g := calibratedFilterG(v.restingFilterG, Diode)
	G := g / (1 + g)
	k := 17 * v.params.Resonance
	invDen3 := 1 / (1 - G*G/2)
	a3 := (G / 2) * invDen3
	invDen2 := 1 / (1 - G*a3/2)
	a2 := (G / 2) * invDen2
	c := G * a3 * a2
	v.restingDiode = restingDiodeShape{
		G: G, k: k, inv: 1 / (1 + g), a3: a3, a2: a2,
		invDen3: invDen3, invDen2: invDen2,
		invFirstDen:   1 / (1 - G*a2/2 + G*k*c/2),
		invRefinedDen: 1 / (1 - G*a2/2),
		gain:          1 + .35*v.params.Resonance,
	}
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
		v.oscA = v.oscillator.selectTables(v.pitchLog)
		if v.params.Detune > 0 {
			v.oscB = v.oscillator.selectTables(v.pitchLog + v.params.Detune/1200)
		}
		if v.params.Sub > 0 {
			v.oscSub = v.oscillator.selectTables(v.pitchLog - 1)
		}
		v.lastPitchLog = v.pitchLog
		v.pitchCached = true
	}
	delta := v.pitchDelta
	saw := v.oscA.saw(v.phaseA)
	osc := saw
	if v.params.Wave != 0 {
		shiftedPhase := v.phaseA - v.params.PulseWidth
		if shiftedPhase < 0 {
			shiftedPhase++
		}
		square := v.oscA.saw(shiftedPhase) - saw + 2*v.params.PulseWidth - 1
		osc = saw*(1-v.params.Wave) + square*v.params.Wave
	}
	v.phaseA = fraction(v.phaseA + delta)
	if v.params.Detune > 0 {
		secondDelta := min(delta*v.detuneRatio, .49)
		second := v.oscB.pulse(v.phaseB, v.params.PulseWidth)
		osc += .5 * second
		v.phaseB = fraction(v.phaseB + secondDelta)
	}
	if v.params.Sub > 0 {
		subDelta := delta * .5
		osc += v.params.Sub * .5 * v.oscSub.pulse(v.phaseSub, .5)
		v.phaseSub = fraction(v.phaseSub + subDelta)
	}
	pre := fastmath.Tanh(v.drivePre*osc) * v.drivePost
	first, second := v.up.Upsample(pre)
	g := v.restingFilterG
	if v.meg != 0 {
		cutoff := v.cutoffHz()
		g = fastmath.TanSmall(math.Pi * cutoff / (2 * v.sampleRate))
	}
	if v.meg == 0 && v.filterBlend == 0 && v.savageBlend == 0 {
		first, second = v.filterRestingDiodePair(first, second)
	} else {
		first, second = v.filterPair(first, second, g)
	}
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

// filterPair shares the per-sample coefficients between the two oversampled
// phases. Filter state still advances once for each phase, in the same order.
func (v *Voice) filterPair(first, second, baseG float64) (float64, float64) {
	savage := v.savageBlend
	if v.filterBlend == 0 {
		g := calibratedFilterG(baseG, Diode)
		G := g / (1 + g)
		k := 17 * v.params.Resonance * (1 + .25*savage)
		gain := 1 + .35*v.params.Resonance*(1-savage)
		invDen3 := 1 / (1 - G*G/2)
		a3 := (G / 2) * invDen3
		invDen2 := 1 / (1 - G*a3/2)
		a2 := (G / 2) * invDen2
		c := G * a3 * a2
		inv := 1 / (1 + g)
		invFirstDen := 1 / (1 - G*a2/2 + G*k*c/2)
		invRefinedDen := 1 / (1 - G*a2/2)
		if savage == 0 {
			first = v.diode.processDiodeShapedNormal(first, G, k, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen) * gain
			second = v.diode.processDiodeShapedNormal(second, G, k, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen) * gain
			return first, second
		}
		first = v.diode.processDiodeShaped(first, G, k, savage, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen) * gain
		second = v.diode.processDiodeShaped(second, G, k, savage, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen) * gain
		return first, second
	}
	if v.filterBlend == 1 {
		g := calibratedFilterG(baseG, Ladder)
		G := g / (1 + g)
		k := 4 * v.params.Resonance * (1 + .25*savage)
		gain := 1 + .5*k*(1-savage)
		first = v.ladder.processLadder(first, G, g, k, savage) * gain
		second = v.ladder.processLadder(second, G, g, k, savage) * gain
		return first, second
	}
	gDiode := calibratedFilterG(baseG, Diode)
	GDiode := gDiode / (1 + gDiode)
	kDiode := 17 * v.params.Resonance * (1 + .25*savage)
	diodeGain := 1 + .35*v.params.Resonance*(1-savage)
	gLadder := calibratedFilterG(baseG, Ladder)
	GLadder := gLadder / (1 + gLadder)
	kLadder := 4 * v.params.Resonance * (1 + .25*savage)
	ladderGain := 1 + .5*kLadder*(1-savage)
	d1 := v.diode.processDiode(first, GDiode, gDiode, kDiode, savage)
	l1 := v.ladder.processLadder(first, GLadder, gLadder, kLadder, savage)
	d1 *= diodeGain
	l1 *= ladderGain
	first = d1*(1-v.filterBlend) + l1*v.filterBlend
	d2 := v.diode.processDiode(second, GDiode, gDiode, kDiode, savage)
	l2 := v.ladder.processLadder(second, GLadder, gLadder, kLadder, savage)
	d2 *= diodeGain
	l2 *= ladderGain
	second = d2*(1-v.filterBlend) + l2*v.filterBlend
	return first, second
}

func (v *Voice) filterRestingDiodePair(first, second float64) (float64, float64) {
	c := &v.restingDiode
	first = v.diode.processDiodeShapedNormal(first, c.G, c.k, c.inv, c.a3, c.a2, c.invDen3, c.invDen2, c.invFirstDen, c.invRefinedDen) * c.gain
	second = v.diode.processDiodeShapedNormal(second, c.G, c.k, c.inv, c.a3, c.a2, c.invDen3, c.invDen2, c.invFirstDen, c.invRefinedDen) * c.gain
	return first, second
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
