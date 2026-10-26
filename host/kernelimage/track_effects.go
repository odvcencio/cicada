//go:build !tinygo || trackfx

package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/fx/pro"
)

const enabledTrackEffectsCapability = TrackEffectsCapability

func writeTrackEffects(w *writer, rate int, specs []engine.TrackEffectConfig) error {
	if len(specs) > engine.MaxTrackInserts {
		return imageError("track insert image exceeds eight stages")
	}
	w.byte(byte(len(specs)))
	for _, p := range specs {
		if err := p.Validate(rate); err != nil {
			return err
		}
		w.byte(byte(p.Kind))
		switch p.Kind {
		case engine.TrackEffectDrive:
			w.byte(byte(p.Drive.Shape))
			w.f64(p.Drive.GainDB)
			w.f64(p.Drive.ToneHz)
			w.f64(p.Drive.Mix)
		case engine.TrackEffectEQ:
			w.byte(boolByte(p.EQ.Enabled))
			w.byte(byte(p.EQ.Type))
			w.f64(p.EQ.FrequencyHz)
			w.f64(p.EQ.Q)
			w.f64(p.EQ.GainDB)
		case engine.TrackEffectComp:
			w.byte(byte(p.Comp.Detect))
			w.byte(boolByte(p.Comp.MakeupAuto))
			for _, v := range []float64{p.Comp.Threshold, p.Comp.Ratio, p.Comp.Knee, p.Comp.AttackMs, p.Comp.ReleaseMs, p.Comp.MakeupDB, p.Comp.Mix} {
				w.f64(v)
			}
		case engine.TrackEffectAmpCab:
			w.byte(byte(p.AmpCab.Cabinet))
			w.byte(boolByte(p.AmpCab.Bypass))
			for _, v := range []float64{p.AmpCab.Drive, p.AmpCab.Tone, p.AmpCab.Output} {
				w.f64(v)
			}
		case engine.TrackEffectDeesser:
			for _, v := range []float64{p.Deesser.FrequencyHz, p.Deesser.ThresholdDB, p.Deesser.Ratio, p.Deesser.RangeDB, p.Deesser.AttackMs, p.Deesser.ReleaseMs, p.Deesser.Mix} {
				w.f64(v)
			}
		}
	}
	return nil
}
func readTrackEffects(r *reader, rate int) ([]engine.TrackEffectConfig, error) {
	count, err := r.byte()
	if err != nil {
		return nil, err
	}
	if count > engine.MaxTrackInserts {
		return nil, imageError("track insert image exceeds eight stages")
	}
	if count == 0 {
		return nil, nil
	}
	out := make([]engine.TrackEffectConfig, int(count))
	for i := range out {
		p := &out[i]
		kind, err := r.byte()
		if err != nil {
			return nil, err
		}
		p.Kind = engine.TrackEffectKind(kind)
		var values []*float64
		switch p.Kind {
		case engine.TrackEffectDrive:
			shape, err := r.byte()
			if err != nil {
				return nil, err
			}
			p.Drive = &fx.DriveParams{Shape: fx.DriveShape(shape)}
			values = []*float64{&p.Drive.GainDB, &p.Drive.ToneHz, &p.Drive.Mix}
		case engine.TrackEffectEQ:
			enabled, err := r.byte()
			if err != nil || enabled > 1 {
				return nil, imageError("invalid EQ enabled flag")
			}
			kind, err := r.byte()
			if err != nil {
				return nil, err
			}
			p.EQ = &pro.Band{Enabled: enabled == 1, Type: pro.EQType(kind)}
			values = []*float64{&p.EQ.FrequencyHz, &p.EQ.Q, &p.EQ.GainDB}
		case engine.TrackEffectComp:
			detect, err := r.byte()
			if err != nil {
				return nil, err
			}
			auto, err := r.byte()
			if err != nil || auto > 1 {
				return nil, imageError("invalid track compressor makeup flag")
			}
			p.Comp = &fx.CompParams{Detect: fx.CompDetector(detect), MakeupAuto: auto == 1}
			values = []*float64{&p.Comp.Threshold, &p.Comp.Ratio, &p.Comp.Knee, &p.Comp.AttackMs, &p.Comp.ReleaseMs, &p.Comp.MakeupDB, &p.Comp.Mix}
		case engine.TrackEffectAmpCab:
			cabinet, err := r.byte()
			if err != nil {
				return nil, err
			}
			bypass, err := r.byte()
			if err != nil || bypass > 1 {
				return nil, imageError("invalid amp bypass")
			}
			p.AmpCab = &ampcab.Params{Cabinet: int(cabinet), Bypass: bypass == 1}
			values = []*float64{&p.AmpCab.Drive, &p.AmpCab.Tone, &p.AmpCab.Output}
		case engine.TrackEffectDeesser:
			p.Deesser = &fx.DeesserParams{}
			values = []*float64{&p.Deesser.FrequencyHz, &p.Deesser.ThresholdDB, &p.Deesser.Ratio, &p.Deesser.RangeDB, &p.Deesser.AttackMs, &p.Deesser.ReleaseMs, &p.Deesser.Mix}
		default:
			return nil, imageError("unknown track insert image kind")
		}
		for _, dst := range values {
			v, err := r.f64()
			if err != nil {
				return nil, err
			}
			*dst = v
		}
		if err := p.Validate(rate); err != nil {
			return nil, err
		}
	}
	return out, nil
}
