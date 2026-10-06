// Package organ implements a bounded tonewheel organ and rotating speaker.
// One audio owner must serialize controls, note events, and rendering.
package organ

import "math"

const (
	MinNote    = 21
	MaxNote    = 108
	MaxVoices  = 8
	wheelMin   = 24
	wheelMax   = 114
	wheelCount = wheelMax - wheelMin + 1
	tableBits  = 11
	tableSize  = 1 << tableBits
	ringSize   = 2048
)

type Error string

func (e Error) Error() string { return string(e) }

// Percussion selects the transient harmonic. Its envelope is shared by all
// keys: a legato note does not retrigger it until every key has been released.
type Percussion uint8

const (
	PercussionOff Percussion = iota
	PercussionSecond
	PercussionThird
)

// Scanner selects three depths of scanner vibrato or dry/wet scanner chorus.
type Scanner uint8

const (
	ScannerOff Scanner = iota
	V1
	V2
	V3
	C1
	C2
	C3
)

// Params describes an original organ registration and its shared effects.
// Drawbars are 0..8 in the order 16', 5 1/3', 8', 4', 2 2/3', 2',
// 1 3/5', 1 1/3', 1'. All float controls except Gain are normalized 0..1.
// Gain is a linear output multiplier in 0..2. RotaryFast selects the fast
// motor target; the horn and drum retain independent acceleration histories.
type Params struct {
	Drawbars       [9]uint8
	Percussion     Percussion
	PercussionFast bool
	PercussionSoft bool
	Scanner        Scanner
	KeyClick       float32
	Leakage        float32
	Crosstalk      float32
	Drive          float32
	RotaryMix      float32
	MicSpread      float32
	RotaryFast     bool
	Gain           float32
}

func DefaultParams() Params {
	return Params{
		Drawbars:   [9]uint8{7, 4, 8, 6, 1, 2, 0, 1, 1},
		Percussion: PercussionThird, PercussionFast: true, PercussionSoft: true,
		Scanner: C2, KeyClick: .32, Leakage: .12, Crosstalk: .08,
		Drive: .18, RotaryMix: .8, MicSpread: .7, Gain: .8,
	}
}

// Patch returns an original registration; no factory patch data is used.
func Patch(name string) (Params, bool) {
	p := DefaultParams()
	switch name {
	case "tonewheel_organ":
	case "organ_jazz":
		p.Drawbars = [9]uint8{7, 2, 8, 4, 0, 1, 0, 0, 0}
		p.PercussionFast, p.PercussionSoft, p.Scanner = false, true, C1
		p.Drive, p.RotaryMix = .08, .65
	case "organ_full":
		p.Drawbars = [9]uint8{8, 6, 8, 7, 5, 6, 3, 4, 5}
		p.Percussion, p.Scanner, p.RotaryFast = PercussionOff, C3, true
		p.Drive, p.Gain = .42, .65
	case "organ_soft":
		p.Drawbars = [9]uint8{6, 0, 7, 3, 0, 2, 0, 0, 1}
		p.Percussion, p.Scanner, p.KeyClick = PercussionOff, V1, .12
		p.Drive, p.RotaryMix = .03, .55
	default:
		return Params{}, false
	}
	return p, true
}

type key struct {
	wheel             [9]uint8
	percussion        [2]uint8
	leakLow, leakHigh uint8
}

type voice struct {
	note                        uint8
	active, held                bool
	age                         uint64
	contactAge                  int
	contact                     [9]float32
	gate, click, clickLow, last float32
	noise                       uint32
}

// Instrument has eight fixed note slots and one continuously running shared
// wheel bank. New prepares every coefficient and key map. Controls, note
// events, and NextStereo allocate nothing and do no coefficient design.
type Instrument struct {
	rate                                                                 int
	voiceLimit                                                           int
	params                                                               Params
	keys                                                                 [MaxNote - MinNote + 1]key
	voices                                                               [MaxVoices]voice
	held                                                                 [MaxNote - MinNote + 1]bool
	heldCount                                                            int
	wheelPhase, wheelStep                                                [wheelCount]uint32
	wheelValue, wheelGain                                                [wheelCount]float32
	sine                                                                 [tableSize + 1]float32
	drawbar                                                              [9]float32
	drawbarTarget                                                        [9]float32
	drawbarAlpha                                                         float32
	contactDelay                                                         [9]int
	attackAlpha, releaseAlpha, clickDecay, clickAlpha                    float32
	percussion, percussionDecay, percussionGain                          float32
	sustain                                                              float32
	serial                                                               uint64
	stealTail, stealDecay                                                float32
	scannerPhase                                                         uint32
	scannerStep                                                          uint32
	scannerA, scannerDepth                                               float32
	scannerChorus                                                        bool
	scannerState                                                         [18]float32
	scannerTap                                                           [19]float32
	crossoverAlpha, crossover1, crossover2                               float32
	hornRing, drumRing                                                   [ringSize]float32
	ringHead                                                             int
	hornPhase, drumPhase, hornSpeed, drumSpeed                           float32
	hornSlow, hornFast, drumSlow, drumFast                               float32
	hornRise, hornFall, drumRise, drumFall                               float32
	inverseRate, hornDelay, hornSwing, drumDelay, drumSwing              float32
	driveAmount, driveCompensation, dcPole, dcInL, dcInR, dcOutL, dcOutR float32
	idleFrames                                                           int
}

func unit(value float32) bool { return value >= 0 && value <= 1 }

func validate(p Params) error {
	for _, d := range p.Drawbars {
		if d > 8 {
			return Error("organ drawbar must be 0..8")
		}
	}
	if p.Percussion > PercussionThird || p.Scanner > C3 {
		return Error("organ percussion or scanner selection is invalid")
	}
	if !unit(p.KeyClick) || !unit(p.Leakage) || !unit(p.Crosstalk) || !unit(p.Drive) || !unit(p.RotaryMix) || !unit(p.MicSpread) || !(p.Gain >= 0 && p.Gain <= 2) {
		return Error("organ normalized controls must be 0..1 and gain 0..2")
	}
	return nil
}

func New(rate int, params Params) (*Instrument, error) {
	if rate != 44100 && rate != 48000 && rate != 96000 && rate != 192000 {
		return nil, Error("organ sample rate must be 44100, 48000, 96000, or 192000")
	}
	if err := validate(params); err != nil {
		return nil, err
	}
	p := &Instrument{rate: rate, params: params, voiceLimit: MaxVoices}
	fs := float64(rate)
	alpha := func(seconds float64) float32 { return float32(1 - math.Exp(-1/(seconds*fs))) }
	p.attackAlpha, p.releaseAlpha = alpha(.0013), alpha(.0035)
	p.drawbarAlpha = alpha(.004)
	p.clickDecay, p.clickAlpha = float32(math.Exp(-1/(.0015*fs))), alpha(1/(2*math.Pi*1600))
	p.stealDecay = float32(math.Exp(-1 / (.0015 * fs)))
	percussionTime := .5
	if params.PercussionFast {
		percussionTime = .105
	}
	p.percussionDecay = float32(math.Exp(-1 / (percussionTime * fs)))
	p.percussionGain = .24
	if params.PercussionSoft {
		p.percussionGain = .09
	}
	p.scannerA = float32((math.Tan(math.Pi*2400/fs) - 1) / (math.Tan(math.Pi*2400/fs) + 1))
	p.scannerStep = uint32(6.9/fs*4294967296 + .5)
	if params.Scanner != ScannerOff {
		depth := (int(params.Scanner) - 1) % 3
		p.scannerDepth = [...]float32{5, 10, 17}[depth]
		p.scannerChorus = params.Scanner >= C1
	}
	p.crossoverAlpha = float32(1 - math.Exp(-2*math.Pi*800/fs))
	p.inverseRate = float32(1 / fs)
	p.hornSlow, p.hornFast, p.drumSlow, p.drumFast = .77, 6.7, .66, 6.0
	p.hornRise, p.hornFall, p.drumRise, p.drumFall = alpha(.9), alpha(1.3), alpha(3.3), alpha(4.2)
	p.hornSpeed, p.drumSpeed, p.drumPhase = p.hornSlow, p.drumSlow, .31
	if params.RotaryFast {
		p.hornSpeed, p.drumSpeed = p.hornFast, p.drumFast
	}
	p.hornDelay, p.hornSwing, p.drumDelay, p.drumSwing = float32(.0025*fs), float32(.00035*fs), float32(.0034*fs), float32(.0007*fs)
	p.driveAmount = 1 + float32(7*params.Drive)
	p.driveCompensation = 1 / p.driveAmount
	p.dcPole = float32(math.Exp(-2 * math.Pi * 8 / fs))
	for i := range p.sine {
		p.sine[i] = float32(math.Sin(2 * math.Pi * float64(i) / tableSize))
	}
	// The final endpoint is exact: interpolation never crosses a tiny libm
	// sine residue at 2*pi. Float32 products separate all render roundings.
	p.sine[tableSize] = 0
	for i := range p.wheelStep {
		hz := 440 * math.Exp2(float64(i+wheelMin-69)/12)
		p.wheelStep[i] = uint32(hz/fs*4294967296 + .5)
		p.wheelPhase[i] = uint32(i+1) * 2654435761
		p.wheelGain[i] = float32(1 / math.Sqrt(1+math.Pow(hz/10500, 2)))
	}
	for i := range p.keys {
		k := &p.keys[i]
		for j, offset := range [...]int{-12, 7, 0, 12, 19, 24, 28, 31, 36} {
			k.wheel[j] = uint8(fold(i+MinNote+offset) - wheelMin)
		}
		k.percussion[0], k.percussion[1] = uint8(fold(i+MinNote+12)-wheelMin), uint8(fold(i+MinNote+19)-wheelMin)
		k.leakLow, k.leakHigh = uint8(fold(i+MinNote-1)-wheelMin), uint8(fold(i+MinNote+1)-wheelMin)
	}
	for i := range p.contactDelay {
		p.contactDelay[i] = int(float64(i%4) * .00023 * fs)
	}
	_ = p.SetDrawbars(params.Drawbars)
	p.drawbar = p.drawbarTarget
	p.Reset()
	return p, nil
}

// Reset clears notes, pedals, effect tails, and generator phases while
// retaining the registration and prepared sample-rate coefficients.
func (p *Instrument) Reset() {
	p.voices = [MaxVoices]voice{}
	p.held = [MaxNote - MinNote + 1]bool{}
	p.heldCount, p.serial, p.sustain, p.percussion, p.stealTail = 0, 0, 0, 0, 0
	for i := range p.wheelPhase {
		p.wheelPhase[i] = uint32(i+1) * 2654435761
	}
	p.wheelValue = [wheelCount]float32{}
	p.drawbar = p.drawbarTarget
	p.scannerPhase, p.scannerState, p.scannerTap = 0, [18]float32{}, [19]float32{}
	p.crossover1, p.crossover2, p.ringHead = 0, 0, 0
	p.hornRing, p.drumRing = [ringSize]float32{}, [ringSize]float32{}
	p.hornPhase, p.drumPhase, p.hornSpeed, p.drumSpeed = 0, .31, p.hornSlow, p.drumSlow
	if p.params.RotaryFast {
		p.hornSpeed, p.drumSpeed = p.hornFast, p.drumFast
	}
	p.dcInL, p.dcInR, p.dcOutL, p.dcOutR = 0, 0, 0, 0
	p.idleFrames = p.rate/4 + 1
}

func fold(note int) int {
	for note < wheelMin {
		note += 12
	}
	for note > wheelMax {
		note -= 12
	}
	return note
}

// SetDrawbars updates attenuation without oscillator or filter redesign.
func (p *Instrument) SetDrawbars(drawbars [9]uint8) error {
	for _, value := range drawbars {
		if value > 8 {
			return Error("organ drawbar must be 0..8")
		}
	}
	// Three dB per stop, with stop zero disconnected rather than attenuated.
	levels := [...]float32{0, .08838835, .125, .1767767, .25, .3535534, .5, .70710677, 1}
	for i, value := range drawbars {
		p.drawbarTarget[i] = levels[value]
	}
	p.params.Drawbars = drawbars
	return nil
}

func (p *Instrument) SetRotaryFast(fast bool) { p.params.RotaryFast = fast }

// SetVoiceLimit bounds allocated note slots to 1..8. Reducing the limit
// clears excluded voices while retaining physical key and percussion state.
// Reset preserves this limit. Hosts may divide a shared voice budget.
func (p *Instrument) SetVoiceLimit(limit int) error {
	if limit < 1 || limit > MaxVoices {
		return Error("organ voice limit must be 1..8")
	}
	for i := limit; i < MaxVoices; i++ {
		p.voices[i] = voice{}
	}
	p.voiceLimit = limit
	return nil
}

// SetSustain uses a switch at 0.5. It extends note gates but does not change
// percussion's physical-key rearm circuit. A console organ has no dampers.
func (p *Instrument) SetSustain(value float32) error {
	if !unit(value) {
		return Error("organ sustain must be 0..1")
	}
	p.sustain = value
	return nil
}

func (p *Instrument) NoteOn(note, velocity uint8) error {
	if note < MinNote || note > MaxNote || velocity > 127 {
		return Error("organ note or velocity is out of range")
	}
	if velocity == 0 {
		p.NoteOff(note)
		return nil
	}
	keyIndex := int(note) - MinNote
	if !p.held[keyIndex] {
		if p.heldCount == 0 && p.params.Percussion != PercussionOff {
			p.percussion = 1
		}
		p.held[keyIndex], p.heldCount = true, p.heldCount+1
	}
	index := -1
	for i := 0; i < p.voiceLimit; i++ {
		if p.voices[i].active && p.voices[i].note == note {
			index = i
			break
		}
	}
	if index < 0 {
		for i := 0; i < p.voiceLimit; i++ {
			if !p.voices[i].active {
				index = i
				break
			}
		}
	}
	if index < 0 {
		index = 0
		for i := 1; i < p.voiceLimit; i++ {
			v, b := &p.voices[i], &p.voices[index]
			if !v.held && b.held || v.held == b.held && v.age < b.age {
				index = i
			}
		}
	}
	v := &p.voices[index]
	gate := float32(0)
	if v.active {
		if v.note == note {
			gate = v.gate
		} else {
			p.stealTail += v.last
		}
	}
	p.serial++
	*v = voice{note: note, active: true, held: true, age: p.serial, gate: gate, click: .012 * float32(p.params.KeyClick*float32(.6+float32(velocity)/127)), noise: uint32(note)*747796405 + uint32(p.serial)*2891336453}
	p.idleFrames = 0
	return nil
}

func (p *Instrument) NoteOff(note uint8) {
	if note < MinNote || note > MaxNote {
		return
	}
	i := int(note) - MinNote
	if p.held[i] {
		p.held[i], p.heldCount = false, p.heldCount-1
	}
	for i := range p.voices {
		v := &p.voices[i]
		if v.active && v.note == note && v.held {
			v.held = false
			v.click += .004 * p.params.KeyClick
		}
	}
}

func (p *Instrument) AllNotesOff() {
	p.held = [MaxNote - MinNote + 1]bool{}
	p.heldCount = 0
	for i := range p.voices {
		p.voices[i].held = false
	}
}

func (p *Instrument) phaseSine(phase uint32) float32 {
	index := phase >> (32 - tableBits)
	fraction := float32(phase&((1<<(32-tableBits))-1)) / float32(1<<(32-tableBits))
	return p.sine[index] + float32(fraction*float32(p.sine[index+1]-p.sine[index]))
}

func (p *Instrument) cycleSine(cycle float32) float32 {
	if cycle >= 1 {
		cycle -= 1
	}
	if cycle < 0 {
		cycle += 1
	}
	t := float32(cycle * tableSize)
	i := int(t)
	return p.sine[i] + float32(float32(t-float32(i))*float32(p.sine[i+1]-p.sine[i]))
}

func (p *Instrument) nextVoice(v *voice) float32 {
	k := &p.keys[int(v.note)-MinNote]
	target := float32(0)
	alpha := p.releaseAlpha
	if v.held || p.sustain >= .5 {
		target, alpha = 1, p.attackAlpha
	}
	v.gate += float32(alpha * float32(target-v.gate))
	var tone float32
	for i, index := range k.wheel {
		if v.contactAge >= p.contactDelay[i] {
			v.contact[i] += float32(p.attackAlpha * float32(1-v.contact[i]))
		}
		if i == 8 && p.params.Percussion != PercussionOff {
			continue
		}
		level := float32(p.drawbar[i] * v.contact[i])
		tone += float32(level * p.wheelValue[index])
		// Crosstalk rides the selected pickup circuit; leakage rides the key.
		if p.params.Crosstalk > 0 {
			neighbor := (int(index) + 1) % wheelCount
			tone += float32(float32(.008*float32(p.params.Crosstalk*level)) * p.wheelValue[neighbor])
		}
	}
	if p.params.Leakage > 0 {
		tone += float32(float32(.018*p.params.Leakage) * float32(p.wheelValue[k.leakLow]+p.wheelValue[k.leakHigh]))
	}
	if p.params.Percussion != PercussionOff {
		if !p.params.PercussionSoft {
			tone = float32(.8 * tone)
		}
		index := k.percussion[int(p.params.Percussion)-1]
		tone += float32(float32(5*float32(p.percussion*p.percussionGain)) * p.wheelValue[index])
	}
	tone = float32(float32(.14*tone) * v.gate)
	v.noise ^= v.noise << 13
	v.noise ^= v.noise >> 17
	v.noise ^= v.noise << 5
	n := float32(int32(v.noise)) / 2147483648
	v.clickLow += float32(p.clickAlpha * float32(n-v.clickLow))
	tone += float32(v.click * float32(n-v.clickLow))
	v.click = float32(v.click * p.clickDecay)
	v.last = tone
	if v.contactAge < p.rate {
		v.contactAge++
	}
	if target == 0 && v.gate < .0000001 && v.click < .0000001 {
		v.active = false
	}
	return tone
}

func (p *Instrument) scanner(input float32) float32 {
	if p.params.Scanner == ScannerOff {
		return input
	}
	p.scannerTap[0] = input
	x := input
	for i := range p.scannerState {
		y := float32(p.scannerA*x) + p.scannerState[i]
		p.scannerState[i] = x - float32(p.scannerA*y)
		p.scannerTap[i+1], x = y, y
	}
	p.scannerPhase += p.scannerStep
	position := float32(float32(.5+float32(.5*p.phaseSine(p.scannerPhase))) * p.scannerDepth)
	i := int(position)
	wet := p.scannerTap[i] + float32(float32(position-float32(i))*float32(p.scannerTap[i+1]-p.scannerTap[i]))
	if p.scannerChorus {
		return float32(.5 * float32(input+wet))
	}
	return wet
}

func delayed(ring *[ringSize]float32, head int, delay float32) float32 {
	n := int(delay)
	fraction := delay - float32(n)
	i := (head - n) & (ringSize - 1)
	j := (i - 1) & (ringSize - 1)
	return ring[i] + float32(fraction*float32(ring[j]-ring[i]))
}

func (p *Instrument) rotary(input float32) (float32, float32) {
	hTarget, dTarget := p.hornSlow, p.drumSlow
	hAlpha, dAlpha := p.hornFall, p.drumFall
	if p.params.RotaryFast {
		hTarget, dTarget, hAlpha, dAlpha = p.hornFast, p.drumFast, p.hornRise, p.drumRise
	}
	p.hornSpeed += float32(hAlpha * float32(hTarget-p.hornSpeed))
	p.drumSpeed += float32(dAlpha * float32(dTarget-p.drumSpeed))
	p.hornPhase += float32(p.hornSpeed * p.inverseRate)
	p.drumPhase -= float32(p.drumSpeed * p.inverseRate)
	if p.hornPhase >= 1 {
		p.hornPhase -= 1
	}
	if p.drumPhase < 0 {
		p.drumPhase += 1
	}
	p.crossover1 += float32(p.crossoverAlpha * float32(input-p.crossover1))
	p.crossover2 += float32(p.crossoverAlpha * float32(p.crossover1-p.crossover2))
	p.hornRing[p.ringHead], p.drumRing[p.ringHead] = input-p.crossover2, p.crossover2
	spread := float32(.22 * p.params.MicSpread)
	var output [2]float32
	for i, offset := range [...]float32{-spread, spread} {
		hSin, dSin := p.cycleSine(p.hornPhase+offset), p.cycleSine(p.drumPhase-offset)
		hCos, dCos := p.cycleSine(p.hornPhase+offset+.25), p.cycleSine(p.drumPhase-offset+.25)
		h := delayed(&p.hornRing, p.ringHead, p.hornDelay+float32(p.hornSwing*hSin))
		d := delayed(&p.drumRing, p.ringHead, p.drumDelay+float32(p.drumSwing*dSin))
		wet := float32(h*float32(.76+float32(.24*hCos))) + float32(d*float32(.86+float32(.14*dCos)))
		output[i] = input + float32(p.params.RotaryMix*float32(wet-input))
	}
	p.ringHead = (p.ringHead + 1) & (ringSize - 1)
	return output[0], output[1]
}

// NextStereo returns one stereo frame. All multiplication boundaries use
// float32 conversions to prevent native FMA contraction from changing PCM.
func (p *Instrument) NextStereo() (float32, float32) {
	for i := range p.drawbar {
		p.drawbar[i] += float32(p.drawbarAlpha * float32(p.drawbarTarget[i]-p.drawbar[i]))
	}
	for i := range p.wheelPhase {
		p.wheelPhase[i] += p.wheelStep[i]
		p.wheelValue[i] = float32(p.phaseSine(p.wheelPhase[i]) * p.wheelGain[i])
	}
	p.percussion = float32(p.percussion * p.percussionDecay)
	if p.percussion < .0000001 {
		p.percussion = 0
	}
	tone := p.stealTail
	p.stealTail = float32(p.stealTail * p.stealDecay)
	active := false
	for i := range p.voices {
		if p.voices[i].active {
			tone += p.nextVoice(&p.voices[i])
			active = true
		}
	}
	if active {
		p.idleFrames = 0
	} else {
		p.idleFrames++
	}
	if p.idleFrames > p.rate/4 {
		return 0, 0
	}
	tone = p.scanner(tone)
	// A bounded odd rational transfer models preamp saturation without a
	// per-frame transcendental. Low drive retains the additive registration.
	if p.params.Drive > 0 {
		x := float32(tone * p.driveAmount)
		if x > 3 {
			x = 3
		}
		if x < -3 {
			x = -3
		}
		square := float32(x * x)
		saturated := float32(float32(x*float32(27+square)) / float32(27+float32(9*square)))
		tone = float32(saturated * p.driveCompensation)
	}
	l, r := p.rotary(tone)
	yL := float32(l-p.dcInL) + float32(p.dcPole*p.dcOutL)
	yR := float32(r-p.dcInR) + float32(p.dcPole*p.dcOutR)
	p.dcInL, p.dcInR, p.dcOutL, p.dcOutR = l, r, yL, yR
	return float32(yL * p.params.Gain), float32(yR * p.params.Gain)
}
