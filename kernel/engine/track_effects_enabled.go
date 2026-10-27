//go:build !tinygo || trackfx

package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/fx/pro"
	"math"
)

type trackEffectStage struct {
	drive   *fx.Drive
	eq      *pro.EQ
	comp    *fx.Compressor
	deesser *fx.Deesser
	amp     *ampcab.Processor
}

const ampControlsEnabled = true

type trackEffectChain struct {
	stages         [MaxTrackInserts]trackEffectStage
	count, latency int
	fault          bool
}

func newTrackEffectChain(rate int, specs []TrackEffectConfig) (*trackEffectChain, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	if len(specs) > MaxTrackInserts {
		return nil, engineError("track insert chain exceeds eight stages")
	}
	c := &trackEffectChain{count: len(specs)}
	for i, p := range specs {
		if err := p.Validate(rate); err != nil {
			return nil, err
		}
		s := &c.stages[i]
		var err error
		switch p.Kind {
		case TrackEffectDrive:
			s.drive, err = fx.NewDrive(rate)
			if err == nil {
				err = s.drive.SetParams(*p.Drive)
			}
			c.latency += fx.DriveLatencyFrames
		case TrackEffectEQ:
			s.eq, err = pro.NewEQ(rate, [4]pro.Band{*p.EQ})
		case TrackEffectComp:
			s.comp, err = fx.NewCompressor(rate)
			if err == nil {
				err = s.comp.SetParams(*p.Comp)
			}
		case TrackEffectDeesser:
			s.deesser, err = fx.NewDeesser(rate, *p.Deesser)
		case TrackEffectAmpCab:
			s.amp, err = ampcab.New(rate, *p.AmpCab)
			c.latency += ampcab.Latency
		}
		if err != nil {
			return nil, err
		}
	}
	c.Reset()
	return c, nil
}
func (c *trackEffectChain) Process(l, r float32) (float32, float32) {
	if c.fault {
		return 0, 0
	}
	for i := 0; i < c.count; i++ {
		s := &c.stages[i]
		switch {
		case s.drive != nil:
			l, r = s.drive.Process(l, r)
			c.fault = s.drive.Fault()
		case s.eq != nil:
			l, r = s.eq.Process(l, r)
		case s.comp != nil:
			l, r = s.comp.Process(l, r)
			c.fault = s.comp.Fault()
		case s.deesser != nil:
			l, r = s.deesser.Process(l, r)
			c.fault = s.deesser.Fault()
		case s.amp != nil:
			l, r = s.amp.Process(l, r)
		}
		if c.fault || math.IsNaN(float64(l)) || math.IsNaN(float64(r)) || math.IsInf(float64(l), 0) || math.IsInf(float64(r), 0) {
			c.fault = true
			return 0, 0
		}
	}
	return l, r
}
func (c *trackEffectChain) Reset() {
	c.fault = false
	for i := 0; i < c.count; i++ {
		s := &c.stages[i]
		if s.drive != nil {
			s.drive.Reset()
		}
		if s.eq != nil {
			s.eq.Reset()
		}
		if s.comp != nil {
			s.comp.Reset()
		}
		if s.deesser != nil {
			s.deesser.Reset()
		}
		if s.amp != nil {
			s.amp.Reset()
		}
	}
}

// ampStage resolves the only amp on this track, refusing ambiguous chains.
func (c *trackEffectChain) ampStage() *ampcab.Processor {
	if c == nil {
		return nil
	}
	var amp *ampcab.Processor
	for i := 0; i < c.count; i++ {
		if c.stages[i].amp != nil {
			if amp != nil {
				return nil
			}
			amp = c.stages[i].amp
		}
	}
	return amp
}

func (c *trackEffectChain) monitorAmpTargets() (AmpTargets, bool) {
	amp := c.ampStage()
	if amp == nil {
		return AmpTargets{}, false
	}
	p := amp.Params()
	return AmpTargets{Drive: p.Drive, Tone: p.Tone, Output: p.Output, Cabinet: p.Cabinet, Bypass: p.Bypass}, true
}
func (c *trackEffectChain) hasAmp() bool { return c.ampStage() != nil }
func (c *trackEffectChain) ampControls() ([3]float64, bool) {
	amp := c.ampStage()
	if amp == nil {
		return [3]float64{}, false
	}
	v := amp.Params()
	return [3]float64{v.Drive, v.Tone, v.Output}, true
}
func (c *trackEffectChain) setAmpControls(v [3]float64, immediate bool) error {
	amp := c.ampStage()
	if amp == nil {
		return engineError("amp controls require exactly one amp insert")
	}
	p := amp.Params()
	p.Drive, p.Tone, p.Output = v[0], v[1], v[2]
	if immediate {
		return amp.SetParamsImmediate(p)
	}
	return amp.SetParams(p)
}
func (c *trackEffectChain) setAmpParam(id kernel.ParamID, value float32, immediate bool) error {
	v, ok := c.ampControls()
	if !ok {
		return engineError("amp controls require exactly one amp insert")
	}
	i := int(id - kernel.ParamAmpDrive)
	if i < 0 || i >= len(v) {
		return engineError("unknown amp control")
	}
	v[i] = float64(value)
	return c.setAmpControls(v, immediate)
}
