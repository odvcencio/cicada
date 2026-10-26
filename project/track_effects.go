package project

import (
	"fmt"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"math"
)

func DeesserParamsFromValues(values map[string]Value) (fx.DeesserParams, error) {
	p := fx.DefaultDeesserParams()
	for _, name := range sortedKeys(values) {
		v := values[name]
		var dst *float64
		unit := "unit"
		switch name {
		case "frequency":
			dst, unit = &p.FrequencyHz, "hz"
		case "threshold":
			dst, unit = &p.ThresholdDB, "db"
		case "ratio":
			dst = &p.Ratio
		case "range":
			dst, unit = &p.RangeDB, "db"
		case "attack":
			dst, unit = &p.AttackMs, "ms"
		case "release":
			dst, unit = &p.ReleaseMs, "ms"
		case "mix":
			dst = &p.Mix
		default:
			return p, fmt.Errorf("unknown de-esser parameter %s", name)
		}
		if v.Number == nil || v.Text != "" || v.Unit != unit || math.IsNaN(*v.Number) || math.IsInf(*v.Number, 0) {
			return p, fmt.Errorf("de-esser %s requires %s", name, unit)
		}
		*dst = *v.Number
	}
	return p, p.Validate()
}

// AmpCabParamsFromValues parses saved track-insert controls.
func AmpCabParamsFromValues(values map[string]Value) (ampcab.Params, error) {
	p := ampcab.DefaultParams()
	for _, name := range sortedKeys(values) {
		v := values[name]
		if name == "bypass" {
			if v.Unit != "enum" || (v.Text != "on" && v.Text != "off" && v.Text != "true" && v.Text != "false") {
				return p, fmt.Errorf("ampcab bypass requires on or off")
			}
			p.Bypass = v.Text == "on" || v.Text == "true"
			continue
		}
		if v.Number == nil || v.Text != "" || math.IsNaN(*v.Number) || math.IsInf(*v.Number, 0) {
			return p, fmt.Errorf("invalid ampcab %s", name)
		}
		switch name {
		case "drive":
			if v.Unit != "unit" {
				return p, fmt.Errorf("ampcab drive requires unitless value")
			}
			p.Drive = *v.Number
		case "tone":
			if v.Unit != "unit" {
				return p, fmt.Errorf("ampcab tone requires unitless value")
			}
			p.Tone = *v.Number
		case "output":
			if v.Unit != "db" {
				return p, fmt.Errorf("ampcab output requires dB")
			}
			p.Output = *v.Number
		case "cabinet":
			if v.Unit != "unit" || math.Trunc(*v.Number) != *v.Number {
				return p, fmt.Errorf("ampcab cabinet requires integer")
			}
			p.Cabinet = int(*v.Number)
		default:
			return p, fmt.Errorf("unknown ampcab parameter %s", name)
		}
	}
	return p, p.Validate()
}
func compileTrackEffect(effect Effect, rate int) (engine.TrackEffectConfig, error) {
	var p engine.TrackEffectConfig
	switch semanticEffectKind(effect) {
	case "drive":
		v, err := DriveParamsFromValues(effect.Params)
		p.Kind, p.Drive = engine.TrackEffectDrive, &v
		return p, err
	case "eq":
		v, err := masterEffectParams(effect)
		p.Kind, p.EQ = engine.TrackEffectEQ, &v.EQ[0]
		if err != nil {
			return p, err
		}
		if p.EQ.FrequencyHz < 10 || p.EQ.FrequencyHz > 19845 {
			return p, fmt.Errorf("track EQ frequency must be 10 to 19845 Hz")
		}
		return p, p.Validate(rate)
	case "comp":
		v, sidechain, err := CompSpecFromValues(effect.Params)
		p.Kind, p.Comp = engine.TrackEffectComp, &v
		if err == nil && sidechain != "" {
			err = fmt.Errorf("CICADA-UNSUPPORTED: track compressor uses its own input; external sidechain is unavailable")
		}
		return p, err
	case "ampcab":
		v, err := AmpCabParamsFromValues(effect.Params)
		p.Kind, p.AmpCab = engine.TrackEffectAmpCab, &v
		return p, err
	case "deesser":
		v, err := DeesserParamsFromValues(effect.Params)
		p.Kind, p.Deesser = engine.TrackEffectDeesser, &v
		return p, err
	}
	return p, fmt.Errorf("CICADA-UNSUPPORTED: %s as a track insert is not implemented", semanticEffectKind(effect))
}
func trackHasInsert(p *Project, id string) bool {
	for _, t := range p.Tracks {
		for _, name := range mixerInsertIDs(t.Mixer) {
			if name == id {
				return true
			}
		}
	}
	return false
}
func mixerInsertIDs(m Mixer) []string {
	if m.Inserts != nil {
		return m.Inserts
	}
	if m.Insert != "none" && m.Insert != "" {
		return []string{m.Insert}
	}
	return nil
}
func musicHasInsert(p *Project, id string) bool {
	for _, bus := range p.Buses {
		if bus.ID == "music" {
			for _, name := range mixerInsertIDs(bus.Mixer) {
				if name == id {
					return true
				}
			}
		}
	}
	return false
}

// TrackHasInsert reports whether the declaration is routed to a track.
func TrackHasInsert(p *Project, id string) bool { return trackHasInsert(p, id) }
