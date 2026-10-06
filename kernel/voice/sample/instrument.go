package sample

import "math"

// Envelope times are milliseconds; Sustain is linear amplitude. Zero times
// are allowed, but the instrument always uses a 2 ms onset/steal ramp.
type Envelope struct {
	Attack, Decay, Sustain, Release float64
}

// Zone is one recorded take. Layer is its velocity centre (1..127). Regions
// in a round-robin group share Group, Layer, key/velocity ranges and Count;
// Position is zero based. Release zones play on dampening, after pedal-up.
type Zone struct {
	ChokeGroup                                 uint8
	OneShot                                    bool
	Region                                     Region
	KeyLow, KeyHigh, VelocityLow, VelocityHigh uint8
	Layer                                      uint8
	Group                                      uint8
	Position, Count                            uint8
	Release                                    bool
	Gain, TuneCents                            float64
}

type Humanize struct {
	DelayMS, Velocity, Cents float64
	Seed                     uint64 `json:"Seed,string"`
}

type InstrumentConfig struct {
	Voices      int
	Amp, Filter Envelope
	// Cutoff is the lowest filter frequency. FilterDepth adds Hz at envelope 1.
	// A zero Cutoff bypasses the filter entirely.
	Cutoff, FilterDepth, Gain, TuneCents float64
	Humanize                             Humanize
}

func DefaultInstrumentConfig() InstrumentConfig {
	return InstrumentConfig{Voices: 16, Amp: Envelope{Attack: 2, Sustain: 1, Release: 180}, Filter: Envelope{Sustain: 1}, Gain: 1}
}

type envState struct {
	level, start float64
	age, stage   int
}

func (e *envState) next(p Envelope, rate int, off bool) float64 {
	if off && e.stage != 3 {
		e.stage, e.age, e.start = 3, 0, e.level
	}
	switch e.stage {
	case 0:
		n := max(1, int(max(2, p.Attack)*float64(rate)/1000))
		e.age++
		e.level = float64(e.age) / float64(n)
		if e.age >= n {
			e.level = 1
			e.stage, e.age = 1, 0
		}
	case 1:
		n := max(1, int(p.Decay*float64(rate)/1000))
		e.age++
		e.level = 1 + (p.Sustain-1)*float64(e.age)/float64(n)
		if e.age >= n {
			e.level = p.Sustain
			e.stage, e.age = 2, 0
		}
	case 2:
		e.level = p.Sustain
	case 3:
		n := max(1, int(p.Release*float64(rate)/1000))
		e.age++
		e.level = e.start * max(0, 1-float64(e.age)/float64(n))
	}
	return e.level
}

type instrumentVoice struct {
	cycle                                 uint64
	releaseAge                            int
	choked                                bool
	attack                                [2]Voice
	release                               [2]Voice
	zones                                 [2]int
	id                                    uint64
	note, velocity                        uint8
	gain                                  [2]float64
	releaseGain                           [2]float64
	amp, filter                           envState
	active, off, deferred, releaseStarted bool
	delay, age                            int
	cutoffLow, cutoffHigh                 float64
	filterL, filterR                      float64
	lastL, lastR, tailL, tailR            float32
	tail                                  int
}

// Instrument owns bounded two-layer note slots and two release layers per
// slot. Construction prepares immutable zones; callbacks never load assets.
type Instrument struct {
	zones  []Zone
	voices []instrumentVoice
	config InstrumentConfig
	rate   int
	serial uint64
	random uint64
	rr     [256]uint64
	pedal  bool
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func validEnvelope(p Envelope) bool {
	return finite(p.Attack) && finite(p.Decay) && finite(p.Sustain) && finite(p.Release) && p.Attack >= 0 && p.Attack <= 30000 && p.Decay >= 0 && p.Decay <= 30000 && p.Release >= 0 && p.Release <= 30000 && p.Sustain >= 0 && p.Sustain <= 1
}

func NewInstrument(rate int, zones []Zone, config InstrumentConfig) (*Instrument, error) {
	if err := validateRate(rate); err != nil {
		return nil, err
	}
	h := config.Humanize
	if len(zones) == 0 || len(zones) > 4096 || config.Voices < 1 || config.Voices > MaxVoices || !validEnvelope(config.Amp) || !validEnvelope(config.Filter) || !finite(config.Gain) || config.Gain < 0 || config.Gain > 16 || !finite(config.TuneCents) || math.Abs(config.TuneCents) > 1200 || !finite(config.Cutoff) || config.Cutoff < 0 || config.Cutoff > float64(rate)/2 || !finite(config.FilterDepth) || config.FilterDepth < 0 || config.FilterDepth > float64(rate)/2 || !finite(h.DelayMS) || h.DelayMS < 0 || h.DelayMS > 100 || !finite(h.Velocity) || h.Velocity < 0 || h.Velocity > 32 || !finite(h.Cents) || h.Cents < 0 || h.Cents > 100 {
		return nil, Error("invalid sample instrument configuration")
	}
	p := &Instrument{rate: rate, config: config, zones: append([]Zone(nil), zones...), voices: make([]instrumentVoice, config.Voices), random: h.Seed}
	for i, z := range p.zones {
		if err := z.Region.Validate(); err != nil {
			return nil, err
		}
		if z.KeyLow > z.KeyHigh || z.KeyHigh > 127 || z.VelocityLow < 1 || z.VelocityLow > z.VelocityHigh || z.VelocityHigh > 127 || z.Layer < z.VelocityLow || z.Layer > z.VelocityHigh || z.Count < 1 || z.Count > 32 || z.Position >= z.Count || !finite(z.Gain) || z.Gain < 0 || z.Gain > 16 || !finite(z.TuneCents) || math.Abs(z.TuneCents) > 1200 || z.Release && z.Region.Loop {
			return nil, Error("invalid sample instrument zone")
		}
		// Check complete cycles and identical mapping for every layer. Ambiguous
		// overlapping groups are rejected rather than depending on input order.
		seen := uint32(0)
		for _, o := range p.zones {
			if z.Release != o.Release || z.Group != o.Group || z.Layer != o.Layer {
				continue
			}
			if o.KeyLow != z.KeyLow || o.KeyHigh != z.KeyHigh || o.VelocityLow != z.VelocityLow || o.VelocityHigh != z.VelocityHigh || o.Count != z.Count || o.ChokeGroup != z.ChokeGroup || o.OneShot != z.OneShot || seen&(1<<o.Position) != 0 {
				return nil, Error("inconsistent or duplicate round robin zone")
			}
			seen |= 1 << o.Position
		}
		if seen != uint32((uint64(1)<<z.Count)-1) {
			return nil, Error("incomplete sample round robin cycle")
		}
		for j := 0; j < i; j++ {
			o := p.zones[j]
			if o.Release == z.Release && o.Group != z.Group && max(o.KeyLow, z.KeyLow) <= min(o.KeyHigh, z.KeyHigh) && max(o.VelocityLow, z.VelocityLow) <= min(o.VelocityHigh, z.VelocityHigh) {
				return nil, Error("overlapping sample groups")
			}
		}
		// Admit the whole key zone plus maximum humanization before publication.
		var v Voice
		v.configure(rate, z.Region)
		for _, key := range []uint8{z.KeyLow, z.KeyHigh} {
			for _, sign := range []float64{-1, 1} {
				v.params.FineTuneCents = config.TuneCents + z.TuneCents + sign*h.Cents
				if _, err := v.playbackRatio(key); err != nil {
					return nil, err
				}
			}
		}
	}
	return p, nil
}

func (p *Instrument) jitter() float64 {
	p.random += 0x9e3779b97f4a7c15
	x := p.random
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return float64(x>>11)/float64(uint64(1)<<53)*2 - 1
}

// selectZones selects the neighbouring velocity centres, then the recorded
// take in each centre. Linear amplitude weights preserve correlated layers.
func (p *Instrument) selectZones(note, velocity uint8, release bool, cycle uint64) ([2]int, [2]float64, bool) {
	indices := [2]int{-1, -1}
	weights := [2]float64{1, 0}
	low, high := -1, -1
	for i, z := range p.zones {
		if z.Release != release || note < z.KeyLow || note > z.KeyHigh || velocity < z.VelocityLow || velocity > z.VelocityHigh || z.Position != uint8(cycle%uint64(z.Count)) {
			continue
		}
		if z.Layer <= velocity && (low < 0 || z.Layer > p.zones[low].Layer) {
			low = i
		}
		if z.Layer >= velocity && (high < 0 || z.Layer < p.zones[high].Layer) {
			high = i
		}
	}
	if low < 0 {
		low = high
	}
	if high < 0 {
		high = low
	}
	if low < 0 {
		return indices, weights, false
	}
	indices[0] = low
	if low != high {
		indices[1] = high
		weights[1] = float64(int(velocity)-int(p.zones[low].Layer)) / float64(int(p.zones[high].Layer)-int(p.zones[low].Layer))
		weights[0] = 1 - weights[1]
	}
	return indices, weights, true
}

func (p *Instrument) NoteOn(note, velocity uint8) (Handle, error) {
	if note > 127 || velocity > 127 || p.serial == ^uint64(0) {
		return Handle{}, Error("sample note, velocity or ID out of range")
	}
	if velocity == 0 {
		return Handle{}, nil
	}
	// Locate group before drawing randomness or advancing round robin state.
	group := -1
	for _, z := range p.zones {
		if !z.Release && note >= z.KeyLow && note <= z.KeyHigh && velocity >= z.VelocityLow && velocity <= z.VelocityHigh {
			group = int(z.Group)
			break
		}
	}
	if group < 0 {
		return Handle{}, Error("sample note has no attack zone")
	}
	h := p.config.Humanize
	vel := uint8(max(1, min(127, int(math.Round(float64(velocity)+p.jitter()*h.Velocity)))))
	// Humanization cannot escape a mapped velocity range.
	zs, weights, ok := p.selectZones(note, vel, false, p.rr[group])
	if !ok || p.zones[zs[0]].Group != uint8(group) {
		vel = velocity
		zs, weights, _ = p.selectZones(note, vel, false, p.rr[group])
	}
	cents := p.jitter() * h.Cents
	delay := int((p.jitter() + 1) * .5 * h.DelayMS * float64(p.rate) / 1000)
	slot := -1
	for i := range p.voices {
		v := &p.voices[i]
		if !v.active && v.tail == 0 {
			slot = i
			break
		}
		if slot < 0 || v.off && !p.voices[slot].off || v.off == p.voices[slot].off && (v.off && v.amp.level < p.voices[slot].amp.level || !v.off && v.id < p.voices[slot].id) {
			slot = i
		}
	}

	choke := p.zones[zs[0]].ChokeGroup
	if choke != 0 {
		for i := range p.voices {
			old := &p.voices[i]
			if old.active && p.zones[old.zones[0]].ChokeGroup == choke {
				old.off, old.choked, old.deferred = true, true, false
			}
		}
	}
	v := &p.voices[slot]
	tl, tr, tail := v.lastL, v.lastR, 0
	if v.active || v.tail > 0 {
		tail = p.rate / 500
	}
	*v = instrumentVoice{active: true, note: note, velocity: vel, zones: zs, delay: delay, cycle: p.rr[group], tailL: tl, tailR: tr, tail: tail}
	p.serial++
	v.id = p.serial
	v.cutoffLow = 1 - math.Exp(-2*math.Pi*p.config.Cutoff/float64(p.rate))
	v.cutoffHigh = 1 - math.Exp(-2*math.Pi*min(float64(p.rate)*.45, p.config.Cutoff+p.config.FilterDepth)/float64(p.rate))
	for i, index := range zs {
		if index < 0 {
			continue
		}
		z := p.zones[index]
		v.attack[i].configure(p.rate, z.Region)
		v.attack[i].params = Params{Gain: 1, FineTuneCents: p.config.TuneCents + z.TuneCents + cents}
		_ = v.attack[i].NoteOn(note, 127)
		v.gain[i] = weights[i] * z.Gain * p.config.Gain * float64(vel) / 127
	}
	p.rr[group]++
	return Handle{Slot: slot, ID: v.id}, nil
}

func (p *Instrument) owned(h Handle) *instrumentVoice {
	if h.Slot < 0 || h.Slot >= len(p.voices) || h.ID == 0 || p.voices[h.Slot].id != h.ID || !p.voices[h.Slot].active {
		return nil
	}
	return &p.voices[h.Slot]
}

func (p *Instrument) releaseNote(v *instrumentVoice) {
	if v.off {
		return
	}
	v.off = true
	v.deferred = false
	if v.delay > 0 {
		v.active = false
		return
	}
	// Match the attack's cycle, so release timbre belongs to the same take.
	zs, weights, ok := p.selectZones(v.note, v.velocity, true, v.cycle)
	if ok {
		for i, index := range zs {
			if index < 0 {
				continue
			}
			z := p.zones[index]
			v.release[i].configure(p.rate, z.Region)
			v.release[i].params = Params{Gain: 1, FineTuneCents: p.config.TuneCents + z.TuneCents}
			_ = v.release[i].NoteOn(v.note, 127)
			v.releaseGain[i] = weights[i] * z.Gain * p.config.Gain * float64(v.velocity) / 127
		}
		v.releaseStarted = true
	}
}

func (p *Instrument) NoteOff(h Handle) bool {
	v := p.owned(h)
	if v == nil {
		return false
	}
	if p.zones[v.zones[0]].OneShot {
		return true
	}
	if p.pedal {
		v.deferred = true
	} else {
		p.releaseNote(v)
	}
	return true
}
func (p *Instrument) Sustain(down bool) {
	p.pedal = down
	if !down {
		for i := range p.voices {
			if p.voices[i].deferred {
				p.releaseNote(&p.voices[i])
			}
		}
	}
}

// Legato preserves sample phase and envelopes inside the existing key zone.
// Cross-zone transitions require a new note or recorded transition samples.
func (p *Instrument) Legato(h Handle, note uint8, cents float64) error {
	v := p.owned(h)
	if v == nil || v.off || !finite(cents) || math.Abs(cents) > 100 {
		return Error("invalid sample legato handle or tuning")
	}
	for _, index := range v.zones {
		if index < 0 {
			continue
		}
		z := p.zones[index]
		if note < z.KeyLow || note > z.KeyHigh {
			return Error("legato crosses a recorded sample zone")
		}
	}
	for i, index := range v.zones {
		if index < 0 {
			continue
		}
		z := p.zones[index]
		if err := v.attack[i].Retune(note, p.config.TuneCents+z.TuneCents+cents); err != nil {
			return err
		}
	}
	v.note = note
	return nil
}

func (p *Instrument) Reset() {
	for i := range p.voices {
		p.voices[i] = instrumentVoice{}
	}
	p.rr = [256]uint64{}
	p.random = p.config.Humanize.Seed
	p.pedal = false
}
func (p *Instrument) ActiveVoices() int {
	n := 0
	for i := range p.voices {
		if p.voices[i].active || p.voices[i].tail > 0 {
			n++
		}
	}
	return n
}
func (p *Instrument) NextStereo() (float32, float32) {
	var sumL, sumR float64
	for i := range p.voices {
		v := &p.voices[i]
		var l, r float64
		if v.active && v.delay > 0 {
			v.delay--
		} else if v.active {
			ampParams := p.config.Amp
			if v.choked {
				ampParams.Release = 2
			}
			amp := v.amp.next(ampParams, p.rate, v.off)
			filter := v.filter.next(p.config.Filter, p.rate, v.off)
			attackActive, releaseActive := false, false
			for j := 0; j < 2; j++ {
				if v.zones[j] >= 0 {
					a, b := v.attack[j].NextStereo()
					l += float64(a) * v.gain[j] * amp
					r += float64(b) * v.gain[j] * amp
					attackActive = attackActive || v.attack[j].Active()
				}
				if v.releaseStarted {
					a, b := v.release[j].NextStereo()
					fade := min(1, float64(v.releaseAge+1)/float64(p.rate/500))
					l += float64(a) * v.releaseGain[j] * fade
					r += float64(b) * v.releaseGain[j] * fade
					releaseActive = releaseActive || v.release[j].Active()
				}
			}
			if p.config.Cutoff > 0 {
				alpha := v.cutoffLow + (v.cutoffHigh-v.cutoffLow)*filter
				v.filterL += float64((l - v.filterL) * alpha)
				v.filterR += float64((r - v.filterR) * alpha)
				l, r = v.filterL, v.filterR
			}
			v.age++
			if v.releaseStarted {
				v.releaseAge++
			}
			if !releaseActive && (!attackActive || v.off && amp == 0) {
				v.active = false
				v.filterL, v.filterR = 0, 0
			}
		}
		if v.tail > 0 {
			fade := float64(v.tail) / float64(p.rate/500)
			l += float64(v.tailL) * fade
			r += float64(v.tailR) * fade
			v.tail--
		}
		v.lastL, v.lastR = float32(l), float32(r)
		sumL += float64(v.lastL)
		sumR += float64(v.lastR)
	}
	return float32(sumL), float32(sumR)
}
func (p *Instrument) Render(left, right []float32) {
	if len(left) != len(right) {
		panic("sample instrument channel lengths differ")
	}
	for i := range left {
		left[i], right[i] = p.NextStereo()
	}
}
