package engine

import (
	"math"
	"math/bits"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/modulate"
)

// RouteSupported reports whether a route may target a parameter. The base
// value of each is readable at load and each reaches its voice through the
// one-pole smoothing path.
func RouteSupported(id kernel.ParamID) bool {
	switch id {
	case kernel.ParamMixGain, kernel.ParamMixPan, kernel.ParamMixSendA, kernel.ParamMixSendB,
		kernel.ParamAcidCutoff, kernel.ParamAcidReso, kernel.ParamAcidEnvmod, kernel.ParamAcidDecay, kernel.ParamAcidAccent:
		return true
	}
	return false
}

type modTarget struct {
	track  uint8
	id     kernel.ParamID
	base   float32
	off    bool
	routes []int
}

type modState struct {
	active     bool
	specs      []ModulatorSpec
	routes     []RouteSpec
	points     [][]modulate.Point
	targets    []modTarget
	fitTicks   []uint64 // whole cycles per loop, synced sources
	fitSamples []uint64 // whole cycles per loop, free-rate sources
	seed       uint32
	values     [MaxModulators]int32
	followers  [MaxModulators]modulate.Follower
	peak       [MaxTracks]float32
	watch      [MaxTracks]bool
	tableLaw   [MaxTracks]bool

	base       int64 // song sample = base + transport sample
	valid      bool
	cycle      int64 // loop pass of the last grid evaluation
	rebase     bool
	wasPlaying bool
	snap       bool
	applying   bool
	loopSample int64 // samples in one loop of the song
	trace      func(grid int64, values []int32)
}

// SetModulationTrace installs a callback that receives every control-grid
// evaluation: the grid point's song sample and the Q30 value of each source.
// Tests and the control log use it; it is nil in normal playback.
func (e *Engine) SetModulationTrace(f func(songSample int64, values []int32)) { e.mod.trace = f }

// ModulatorValue is the latest Q30 value of a source.
func (e *Engine) ModulatorValue(i int) int32 {
	if i < 0 || i >= len(e.mod.specs) {
		return 0
	}
	return e.mod.values[i]
}

func (e *Engine) loadModulation(cfg *Config) error {
	if !modulationEnabled || len(cfg.Modulators) == 0 && len(cfg.Routes) == 0 {
		return nil
	}
	if len(cfg.Modulators) > MaxModulators || len(cfg.Routes) > MaxModRoutes {
		return engineError("too many modulators or routes")
	}
	m := &e.mod
	m.specs = append([]ModulatorSpec(nil), cfg.Modulators...)
	m.routes = append([]RouteSpec(nil), cfg.Routes...)
	m.seed = cfg.Seed
	m.points = make([][]modulate.Point, len(m.specs))
	m.fitTicks = make([]uint64, len(m.specs))
	m.fitSamples = make([]uint64, len(m.specs))
	if e.loopSong && e.scheduleDuration > 0 {
		m.loopSample = e.songSampleAtTick(e.scheduleDuration)
	}
	for i, s := range m.specs {
		switch s.Kind {
		case ModKindLFO:
			if s.Wave > ModWaveSampleHold {
				return engineError("invalid LFO wave")
			}
		case ModKindSway:
			if s.Octaves < 1 || s.Octaves > 4 {
				return engineError("sway octaves must be 1 to 4")
			}
		case ModKindEnvelope:
			if int(s.Source) >= e.tracks {
				return engineError("envelope follows an unknown track")
			}
			m.watch[s.Source] = true
		case ModKindContour:
			n := len(s.Points)
			if n < 2 || n > MaxContourPoints || s.Points[0].At != 0 || s.Points[n-1].At != 1<<32 {
				return engineError("invalid contour")
			}
			pts := make([]modulate.Point, n)
			for j, p := range s.Points {
				if j > 0 && p.At <= s.Points[j-1].At || p.Value < -modulate.One || p.Value > modulate.One || p.Shape > ModShapeExponential {
					return engineError("invalid contour point")
				}
				pts[j] = modulate.Point{At: p.At, Value: p.Value, Shape: modulate.Shape(p.Shape)}
			}
			m.points[i] = pts
		default:
			return engineError("unknown modulator kind")
		}
		if s.Kind != ModKindEnvelope && (s.RateTicks == 0) == (s.RateMicroHz == 0) {
			return engineError("a modulator needs one rate")
		}
		if s.RateTicks > 1<<28 {
			return engineError("modulator period is too long")
		}
		if m.loopSample > 0 && s.Continue {
			if s.RateTicks > 0 {
				period := uint64(s.RateTicks)
				m.fitTicks[i] = max(1, (uint64(e.scheduleDuration)+period/2)/period)
			} else if s.RateMicroHz > 0 {
				hi, lo := bits.Mul64(uint64(m.loopSample), s.RateMicroHz)
				d := uint64(e.sampleRate) * 1_000_000
				q, r := bits.Div64(hi%d, lo, d)
				if r >= d-r {
					q++
				}
				m.fitSamples[i] = max(1, q)
			}
		}
	}
	for ri, r := range m.routes {
		spec, ok := kernel.Param(kernel.ParamID(r.Param))
		if !ok || !RouteSupported(spec.ID) || int(r.Source) >= len(m.specs) || int(r.Track) >= e.tracks || math.IsNaN(float64(r.Depth)) || math.IsInf(float64(r.Depth), 0) {
			return engineError("invalid modulator route")
		}
		if spec.ID >= kernel.ParamAcidCutoff && spec.ID <= kernel.ParamAcidAccent && e.voices[r.Track].kind != VoiceAcid {
			return engineError("acid route on a track without the acid voice")
		}
		found := -1
		for ti, t := range m.targets {
			if t.track == r.Track && t.id == spec.ID {
				found = ti
			}
		}
		if found < 0 {
			base, off := e.routeBase(r.Track, spec.ID)
			m.targets = append(m.targets, modTarget{track: r.Track, id: spec.ID, base: base, off: off})
			found = len(m.targets) - 1
		}
		m.targets[found].routes = append(m.targets[found].routes, ri)
		if spec.ID == kernel.ParamMixGain || spec.ID == kernel.ParamMixPan {
			m.tableLaw[r.Track] = true
		}
	}
	m.active = true
	m.rebase = true
	return nil
}

// routeBase reads a target's configured value once, at load.
func (e *Engine) routeBase(track uint8, id kernel.ParamID) (float32, bool) {
	v := &e.voices[track]
	switch id {
	case kernel.ParamMixGain:
		return v.gainDB, v.sourceOff
	case kernel.ParamMixPan:
		return v.pan, false
	case kernel.ParamMixSendA:
		return v.targetSendA, false
	case kernel.ParamMixSendB:
		return v.targetSendB, false
	case kernel.ParamAcidCutoff:
		return float32(v.acidTarget.Cutoff), false
	case kernel.ParamAcidReso:
		return float32(v.acidTarget.Resonance), false
	case kernel.ParamAcidEnvmod:
		return float32(v.acidTarget.EnvMod), false
	case kernel.ParamAcidDecay:
		return float32(v.acidTarget.Decay * 1000), false
	}
	return float32(v.acidTarget.Accent), false
}

func (m *modState) find(track uint8, id kernel.ParamID) *modTarget {
	for i := range m.targets {
		if m.targets[i].track == track && m.targets[i].id == id {
			return &m.targets[i]
		}
	}
	return nil
}

// effective is the spec's evaluation order for a routed target: the base set by
// default, scene setting, automation or macro, then each route in declaration
// order, then one clamp. Every product is rounded to float32 before it is added,
// so a fused multiply-add on arm64 cannot change the result.
func (e *Engine) modEffective(t *modTarget) float32 {
	value := t.base
	for _, ri := range t.routes {
		r := &e.mod.routes[ri]
		scaled := float32(float32(e.mod.values[r.Source]) * (1.0 / (1 << 30)))
		amount := float32(r.Depth * scaled)
		if r.Multiply {
			value = float32(value * float32(1+amount))
		} else {
			value = float32(value + amount)
		}
	}
	spec := kernel.Params[t.id]
	if value < spec.Min {
		value = spec.Min
	} else if value > spec.Max {
		value = spec.Max
	}
	return value
}

// modPeak records the post-fader peak of a watched track for envelope sources.
func (e *Engine) modPeak(track int, left, right float32) {
	if left < 0 {
		left = -left
	}
	if right < 0 {
		right = -right
	}
	if left > e.mod.peak[track] {
		e.mod.peak[track] = left
	}
	if right > e.mod.peak[track] {
		e.mod.peak[track] = right
	}
}

func (e *Engine) gainLinear(track uint8, gainDB float32) float32 {
	if modulationEnabled && e.mod.tableLaw[track] {
		return mix.DBToLinear(gainDB)
	}
	return float32(math.Pow(10, float64(gainDB)/20))
}

func (e *Engine) trackMix(track uint8, gainDB, pan float32) mix.Track {
	if modulationEnabled && e.mod.tableLaw[track] {
		return mix.NewTrackTable(gainDB, pan)
	}
	return mix.NewTrack(float64(gainDB), float64(pan), false)
}

// nextGridFrames is how many frames, counted from the current one, the next
// control-grid point is away. Native segments end there.
func (e *Engine) nextGridFrames() (int, bool) {
	if !modulationEnabled || !e.mod.active || !e.mod.valid || !e.transport.Playing() {
		return 0, false
	}
	s := e.mod.base + e.transport.Sample()
	return modulate.GridFrames - int(s&(modulate.GridFrames-1)), true
}

// advanceModulation runs once per frame. At a control-grid point (a multiple of
// 128 song samples) it evaluates every source from the exact song position and
// applies the routes. Everything else here is bookkeeping for the song sample:
// the grid is a property of song position, never of the host buffer.
func (e *Engine) advanceModulation() {
	m := &e.mod
	if !m.active {
		return
	}
	if !e.transport.Playing() {
		m.wasPlaying = false
		return
	}
	sample := e.transport.Sample()
	if !m.wasPlaying || m.rebase {
		m.wasPlaying, m.rebase, m.valid, m.snap = true, false, true, true
		m.base = e.songSampleAtTick(e.transport.Tick()) - sample
		for i := range m.followers {
			m.followers[i].Reset()
		}
		m.peak = [MaxTracks]float32{}
		m.values = [MaxModulators]int32{}
		m.cycle = 0
		if e.scheduleDuration > 0 {
			m.cycle = e.transport.Tick() / e.scheduleDuration
		}
	}
	song := m.base + sample
	if song&(modulate.GridFrames-1) != 0 {
		return
	}
	clock := e.transport.Clock()
	tick, rem, den := clock.TickAtSampleRem(sample)
	loopSample := song
	if m.loopSample > 0 {
		cycle := tick / e.scheduleDuration
		if cycle != m.cycle {
			// A loop wrap restarts the one stateful source, as a seek does.
			m.cycle = cycle
			for i := range m.followers {
				m.followers[i].Reset()
			}
			m.peak = [MaxTracks]float32{}
		}
		tick -= cycle * e.scheduleDuration
		loopSample = max(0, song-cycle*m.loopSample)
	}
	hasFollower := false
	for i := range m.specs {
		s := &m.specs[i]
		var pos modulate.Position
		switch {
		case s.Kind == ModKindEnvelope:
			hasFollower = true
			peak := m.peak[s.Source]
			q := float32(peak * (1 << 30))
			level := int32(modulate.One)
			if q < modulate.One {
				level = int32(q)
			}
			m.values[i] = m.followers[i].Step(level, s.Attack, s.Release)
			continue
		case s.RateTicks > 0 && m.fitTicks[i] > 0:
			pos = modulate.FitPosition(uint64(tick), rem, den, uint64(e.scheduleDuration), m.fitTicks[i])
		case s.RateTicks > 0:
			pos = modulate.SyncedPosition(uint64(tick), rem, den, uint64(s.RateTicks))
		case m.fitSamples[i] > 0:
			pos = modulate.FitPosition(uint64(loopSample), 0, 1, uint64(m.loopSample), m.fitSamples[i])
		default:
			pos = modulate.FreePosition(uint64(loopSample), uint32(e.sampleRate), s.RateMicroHz)
		}
		switch s.Kind {
		case ModKindLFO:
			cycles := m.fitTicks[i]
			if cycles == 0 {
				cycles = m.fitSamples[i]
			}
			if cycles != 0 && s.Wave == ModWaveSampleHold {
				m.values[i] = modulate.LFOFitted(modulate.Wave(s.Wave), m.seed, uint8(i), pos.Offset(s.PhaseQ32), cycles)
			} else {
				m.values[i] = modulate.LFO(modulate.Wave(s.Wave), m.seed, uint8(i), pos.Offset(s.PhaseQ32))
			}
		case ModKindSway:
			cycles := m.fitTicks[i]
			if cycles == 0 {
				cycles = m.fitSamples[i]
			}
			if cycles != 0 {
				m.values[i] = modulate.SwayFitted(m.seed, uint8(i), uint(s.Octaves), pos, cycles)
			} else {
				m.values[i] = modulate.Sway(m.seed, uint8(i), uint(s.Octaves), pos)
			}
		default:
			m.values[i] = modulate.Contour(m.points[i], pos)
		}
	}
	if hasFollower {
		m.peak = [MaxTracks]float32{}
	}
	if m.trace != nil {
		m.trace(song, m.values[:len(m.specs)])
	}
	m.applying = true
	for i := range m.targets {
		t := &m.targets[i]
		if t.off {
			continue
		}
		e.setParamMode(cmd.Command{Op: cmd.OpSetParam, Track: t.track, Index: uint16(t.id), Arg0: math.Float32bits(e.modEffective(t))}, m.snap)
	}
	m.applying = false
	m.snap = false
}
