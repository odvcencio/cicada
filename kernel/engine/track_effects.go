package engine

import (
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/pro"
	"math"
)

const MaxTrackInserts = 8

type TrackEffectKind uint8

const (
	TrackEffectDrive TrackEffectKind = iota + 1
	TrackEffectEQ
	TrackEffectComp
	TrackEffectDeesser
)

// TrackEffectConfig is copied into independent, bounded audio-thread state.
// Effects run in authored order before the fader and the existing send taps.
type TrackEffectConfig struct {
	Kind    TrackEffectKind
	Drive   *fx.DriveParams   `json:",omitempty"`
	EQ      *pro.Band         `json:",omitempty"`
	Comp    *fx.CompParams    `json:",omitempty"`
	Deesser *fx.DeesserParams `json:",omitempty"`
}

func (p TrackEffectConfig) Validate(rate int) error {
	count := 0
	for _, present := range []bool{p.Drive != nil, p.EQ != nil, p.Comp != nil, p.Deesser != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return engineError("track effect requires exactly one parameter set")
	}
	switch p.Kind {
	case TrackEffectDrive:
		if p.Drive != nil {
			return p.Drive.Validate()
		}
	case TrackEffectEQ:
		if p.EQ != nil {
			q := p.EQ
			if math.IsNaN(q.FrequencyHz) || math.IsInf(q.FrequencyHz, 0) || math.IsNaN(q.Q) || math.IsInf(q.Q, 0) || math.IsNaN(q.GainDB) || math.IsInf(q.GainDB, 0) || q.Type > pro.Lowpass || q.FrequencyHz < 10 || q.FrequencyHz > .45*float64(rate) || q.Q < .1 || q.Q > 20 || q.GainDB < -24 || q.GainDB > 24 {
				return engineError("track EQ parameter is invalid or out of range")
			}
			return nil
		}
	case TrackEffectComp:
		if p.Comp != nil {
			return p.Comp.Validate()
		}
	case TrackEffectDeesser:
		if p.Deesser != nil {
			return p.Deesser.Validate()
		}
	}
	return engineError("track effect kind and parameters do not match")
}

func trackInsertLatency(p *TrackConfig) int {
	n := 0
	if p.InsertDrive != nil {
		n += fx.DriveLatencyFrames
	}
	for _, s := range p.InsertChain {
		if s.Kind == TrackEffectDrive {
			n += fx.DriveLatencyFrames
		}
	}
	return n
}
