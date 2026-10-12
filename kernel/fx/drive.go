// Package fx implements Cicada's built-in audio effects.
package fx

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
	"m31labs.dev/cicada/kernel/dsp/halfband"
)

// Processor is the sample-level contract for a stereo insert or send effect.
// LatencyFrames lets the mixer align dry audio and other routed paths.
type Processor interface {
	Process(left, right float32) (float32, float32)
	Reset()
	LatencyFrames() int
	Fault() bool
}

type Error string

func (e Error) Error() string { return string(e) }

type DriveShape uint8

const (
	Soft DriveShape = iota
	Hard
	Fold
	Diode
	driveShapes
)

type DriveParams struct {
	Shape  DriveShape
	GainDB float64
	ToneHz float64
	Mix    float64
}

func DefaultDriveParams() DriveParams {
	return DriveParams{Shape: Soft, GainDB: 0, ToneHz: 12_000, Mix: 1}
}

func (p DriveParams) Validate() error {
	if p.Shape >= driveShapes || !finite(p.GainDB) || !finite(p.ToneHz) || !finite(p.Mix) ||
		p.GainDB < 0 || p.GainDB > 36 || p.ToneHz < 1_000 || p.ToneHz > 20_000 || p.Mix < 0 || p.Mix > 1 {
		return Error("drive parameter is invalid or out of range")
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

type stereo struct{ left, right float64 }

// DriveLatencyFrames is the delay from the two FIR filters at 2x sample rate.
const DriveLatencyFrames = (halfband.Taps - 1) / 2

// Drive uses fixed storage, including the dry delay and four oversampled
// paths. All four shapes stay warm so changing shape can crossfade cleanly.
type Drive struct {
	sampleRate             float64
	params                 DriveParams
	table                  [driveShapes][33]float64
	pre, targetPre         float64
	post, targetPost       [driveShapes]float64
	tone, targetTone       float64
	mix, targetMix         float64
	weights, targetWeights [driveShapes]float64
	smooth                 float64
	upLeft, upRight        halfband.FIR
	softLeft, softRight    halfband.FIR
	hardLeft, hardRight    halfband.FIR
	foldLeft, foldRight    halfband.FIR
	diodeLeft, diodeRight  halfband.FIR
	dryDelay               [DriveLatencyFrames]stereo
	position               int
	toneState              [driveShapes]stereo
	fault                  bool
	zeroFrames             int
}

func NewDrive(sampleRate int) (*Drive, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return nil, Error("unsupported drive sample rate")
	}
	d := &Drive{
		sampleRate: float64(sampleRate), smooth: 1 - math.Exp(-1/(.005*float64(sampleRate))),
		upLeft: halfband.New(), upRight: halfband.New(),
		softLeft: halfband.New(), softRight: halfband.New(),
		hardLeft: halfband.New(), hardRight: halfband.New(),
		foldLeft: halfband.New(), foldRight: halfband.New(),
		diodeLeft: halfband.New(), diodeRight: halfband.New(),
	}
	d.buildTables()
	if err := d.SetParams(DefaultDriveParams()); err != nil {
		return nil, err
	}
	d.Reset()
	return d, nil
}

func (d *Drive) Params() DriveParams { return d.params }
func (d *Drive) Fault() bool         { return d.fault }
func (d *Drive) LatencyFrames() int  { return DriveLatencyFrames }

// Silent reports an exact zero signal state after the finite FIR histories
// drain and every tone state is zero. Process must still run to advance
// automated parameter smoothing; silent output alone cannot prove this state.
func (d *Drive) Silent() bool {
	return !d.fault && d.zeroFrames >= halfband.Taps && d.toneState == [driveShapes]stereo{}
}

func (d *Drive) SetParams(p DriveParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	d.params = p
	d.targetPre = math.Pow(10, p.GainDB/20)
	position := p.GainDB * 32 / 36
	index := int(position)
	if index >= 32 {
		index, position = 31, 32
	}
	frac := position - float64(index)
	for shape := range d.targetPost {
		d.targetPost[shape] = float64(d.table[shape][index]*(1-frac)) + float64(d.table[shape][index+1]*frac)
		d.targetWeights[shape] = 0
	}
	d.targetWeights[p.Shape] = 1
	d.targetTone = 1 - math.Exp(-2*math.Pi*p.ToneHz/d.sampleRate)
	d.targetMix = p.Mix
	return nil
}

func (d *Drive) Reset() {
	d.upLeft.Reset()
	d.upRight.Reset()
	d.softLeft.Reset()
	d.softRight.Reset()
	d.hardLeft.Reset()
	d.hardRight.Reset()
	d.foldLeft.Reset()
	d.foldRight.Reset()
	d.diodeLeft.Reset()
	d.diodeRight.Reset()
	d.dryDelay = [DriveLatencyFrames]stereo{}
	d.toneState = [driveShapes]stereo{}
	d.position, d.fault = 0, false
	d.zeroFrames = halfband.Taps
	d.pre, d.post, d.tone, d.mix, d.weights = d.targetPre, d.targetPost, d.targetTone, d.targetMix, d.targetWeights
}

func (d *Drive) buildTables() {
	const amplitude = 0.251188643150958 // -12 dBFS peak reference
	inputRMS := amplitude / math.Sqrt2
	for shape := DriveShape(0); shape < driveShapes; shape++ {
		for index := range d.table[shape] {
			pre := math.Pow(10, float64(index)*36/(32*20))
			var power float64
			for sample := 0; sample < 1024; sample++ {
				x := amplitude * math.Sin(2*math.Pi*float64(sample)/1024)
				y := shapeSample(shape, x*pre)
				power += float64(y * y)
			}
			d.table[shape][index] = inputRMS / math.Sqrt(power/1024)
		}
	}
}

func shapeSample(shape DriveShape, input float64) float64 {
	switch shape {
	case Soft:
		return fastmath.Tanh(input)
	case Hard:
		return math.Max(-1, math.Min(1, input))
	case Fold:
		return math.Sin(math.Pi / 2 * input)
	case Diode:
		if input > 0 {
			return fastmath.Tanh(input)
		}
		return fastmath.Tanh(.7 * input)
	default:
		return 0
	}
}

func (d *Drive) Process(left, right float32) (float32, float32) {
	if d.fault {
		return 0, 0
	}
	x := stereo{float64(left), float64(right)}
	if !finite(x.left) || !finite(x.right) {
		d.fault = true
		return 0, 0
	}
	d.pre += float64((d.targetPre - d.pre) * d.smooth)
	d.tone += float64((d.targetTone - d.tone) * d.smooth)
	d.mix += float64((d.targetMix - d.mix) * d.smooth)
	for shape := range d.post {
		d.post[shape] += float64((d.targetPost[shape] - d.post[shape]) * d.smooth)
		d.weights[shape] += float64((d.targetWeights[shape] - d.weights[shape]) * d.smooth)
	}
	if left == 0 && right == 0 && d.zeroFrames >= halfband.Taps {
		// Each 43-tap filter retains at most 42 oversampled samples. The
		// cascaded up/down pair is therefore empty after 43 base-rate zeros;
		// this also exceeds the 21-frame dry delay. This is a finite-history
		// proof, independent of amplitude and of the active nonlinear shape.
		// Every FIR state and the dry delay is exactly zero. Empty FIR rings may keep
		// their cursor: their coefficients depend on relative age, so the next
		// nonzero input renders identically at any cursor. Keep the explicit dry
		// delay timeline moving, and keep all shape/parameter ramps above warm.
		d.position++
		if d.position == DriveLatencyFrames {
			d.position = 0
		}
		var wet stereo
		for shape := range d.toneState {
			state := &d.toneState[shape]
			state.left += float64((0 - state.left) * d.tone)
			state.right += float64((0 - state.right) * d.tone)
			wet.left += float64(state.left * d.weights[shape])
			wet.right += float64(state.right * d.weights[shape])
		}
		// Tone tails keep their exact per-sample decay; no threshold gates them.
		return float32(wet.left * d.mix), float32(wet.right * d.mix)
	}
	if left == 0 && right == 0 {
		d.zeroFrames++
	} else {
		d.zeroFrames = 0
	}
	index := d.position
	dry := d.dryDelay[index]
	d.dryDelay[index] = x
	d.position++
	if d.position == DriveLatencyFrames {
		d.position = 0
	}
	firstL, secondL := d.upLeft.Upsample(x.left)
	firstR, secondR := d.upRight.Upsample(x.right)
	// Every nonlinear shape needs bandlimiting. Soft saturation still creates
	// harmonics above Nyquist; a base-rate tone filter cannot remove them once
	// they have folded into the audible band. Keep every path warm for switches.
	soft := stereo{
		d.softLeft.Downsample(shapeSample(Soft, firstL*d.pre), shapeSample(Soft, secondL*d.pre)) * d.post[Soft],
		d.softRight.Downsample(shapeSample(Soft, firstR*d.pre), shapeSample(Soft, secondR*d.pre)) * d.post[Soft],
	}
	diode := stereo{
		d.diodeLeft.Downsample(shapeSample(Diode, firstL*d.pre), shapeSample(Diode, secondL*d.pre)) * d.post[Diode],
		d.diodeRight.Downsample(shapeSample(Diode, firstR*d.pre), shapeSample(Diode, secondR*d.pre)) * d.post[Diode],
	}
	hard := stereo{
		d.hardLeft.Downsample(shapeSample(Hard, firstL*d.pre), shapeSample(Hard, secondL*d.pre)) * d.post[Hard],
		d.hardRight.Downsample(shapeSample(Hard, firstR*d.pre), shapeSample(Hard, secondR*d.pre)) * d.post[Hard],
	}
	fold := stereo{
		d.foldLeft.Downsample(shapeSample(Fold, firstL*d.pre), shapeSample(Fold, secondL*d.pre)) * d.post[Fold],
		d.foldRight.Downsample(shapeSample(Fold, firstR*d.pre), shapeSample(Fold, secondR*d.pre)) * d.post[Fold],
	}
	shaped := [driveShapes]stereo{soft, hard, fold, diode}
	var wet stereo
	for shape, signal := range shaped {
		state := &d.toneState[shape]
		state.left += float64((signal.left - state.left) * d.tone)
		state.right += float64((signal.right - state.right) * d.tone)
		wet.left += float64(state.left * d.weights[shape])
		wet.right += float64(state.right * d.weights[shape])
	}
	outL := float64(dry.left*(1-d.mix)) + float64(wet.left*d.mix)
	outR := float64(dry.right*(1-d.mix)) + float64(wet.right*d.mix)
	if !finite(outL) || !finite(outR) || math.Abs(outL) > math.MaxFloat32 || math.Abs(outR) > math.MaxFloat32 {
		d.fault = true
		return 0, 0
	}
	return float32(outL), float32(outR)
}
