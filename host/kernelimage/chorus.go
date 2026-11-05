package kernelimage

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/chorus"
)

func validateChorusAutomation(cfg *engine.Config) error {
	for _, control := range cfg.Automation {
		if control.Index >= uint16(kernel.ParamChorusRate) && control.Index <= uint16(kernel.ParamChorusMix) {
			if int(control.Track) >= cfg.Tracks || !engine.ChorusControlFits(&cfg.Track[control.Track]) {
				return imageError("chorus automation requires exactly one chorus insert")
			}
		}
	}
	return nil
}

// ChorusFlag selects the modular kernel and appends track/stage parameters.
const ChorusFlag uint32 = 1 << 5

func hasChorus(cfg *engine.Config) bool {
	for _, track := range cfg.Track[:cfg.Tracks] {
		for _, stage := range track.InsertChain {
			if stage.Kind == engine.TrackEffectChorus {
				return true
			}
		}
	}
	return false
}

func writeChorus(w *writer, cfg *engine.Config) error {
	count := 0
	for _, track := range cfg.Track[:cfg.Tracks] {
		for _, stage := range track.InsertChain {
			if stage.Kind == engine.TrackEffectChorus {
				count++
			}
		}
	}
	if count == 0 || count > 255 {
		return imageError("invalid chorus record count")
	}
	w.byte(byte(count))
	for track := 0; track < cfg.Tracks; track++ {
		for slot, stage := range cfg.Track[track].InsertChain {
			if stage.Kind != engine.TrackEffectChorus {
				continue
			}
			if err := stage.Validate(cfg.SampleRate); err != nil {
				return err
			}
			w.byte(byte(track))
			w.byte(byte(slot))
			w.byte(byte(stage.Chorus.Voices))
			w.f64(stage.Chorus.Rate)
			w.f64(stage.Chorus.Depth)
			w.f64(stage.Chorus.Spread)
			w.f64(stage.Chorus.Mix)
		}
	}
	return nil
}

func readChorus(r *reader, cfg *engine.Config, rate int) error {
	count, err := r.byte()
	if err != nil || count == 0 {
		return imageError("invalid chorus record count")
	}
	for i := 0; i < int(count); i++ {
		track, err := r.byte()
		if err != nil {
			return err
		}
		slot, err := r.byte()
		if err != nil {
			return err
		}
		voices, err := r.byte()
		if err != nil {
			return err
		}
		if int(track) >= cfg.Tracks || int(slot) >= len(cfg.Track[track].InsertChain) {
			return imageError("invalid chorus stage reference")
		}
		stage := &cfg.Track[track].InsertChain[slot]
		if stage.Kind != engine.TrackEffectChorus || stage.Chorus != nil {
			return imageError("duplicate or mismatched chorus record")
		}
		p := chorus.Params{Voices: int(voices)}
		for _, dst := range []*float64{&p.Rate, &p.Depth, &p.Spread, &p.Mix} {
			*dst, err = r.f64()
			if err != nil {
				return err
			}
		}
		stage.Chorus = &p
		if err := stage.Validate(rate); err != nil {
			return err
		}
	}
	return nil
}
