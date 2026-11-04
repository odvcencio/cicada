package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/expression"
)

// InstrumentVibratoFlag marks the appended per-track vibrato defaults.
const InstrumentVibratoFlag uint32 = 1 << 3

func trackVibrato(t *engine.TrackConfig) *expression.VibratoConfig {
	if t.Vibrato != nil {
		return t.Vibrato
	}
	if t.Kind == engine.VoiceGraph || t.Kind == engine.VoiceGraphPoly {
		return t.Graph.Vibrato
	}
	return nil
}
func hasVibrato(cfg *engine.Config) bool {
	for i := 0; i < cfg.Tracks; i++ {
		if trackVibrato(&cfg.Track[i]) != nil {
			return true
		}
	}
	return false
}
func writeVibrato(w *writer, cfg *engine.Config) error {
	count := 0
	for i := 0; i < cfg.Tracks; i++ {
		if trackVibrato(&cfg.Track[i]) != nil {
			count++
		}
	}
	if count == 0 || count > engine.MaxTracks {
		return imageError("invalid instrument vibrato count")
	}
	w.byte(byte(count))
	for i := 0; i < cfg.Tracks; i++ {
		track := &cfg.Track[i]
		value := trackVibrato(track)
		if value == nil {
			continue
		}
		if track.Vibrato != nil && track.Graph.Vibrato != nil && *track.Vibrato != *track.Graph.Vibrato {
			return imageError("conflicting instrument vibrato")
		}
		if track.Kind != engine.VoiceAcid && track.Kind != engine.VoiceGraph && track.Kind != engine.VoiceGraphPoly {
			return imageError("unsupported instrument vibrato voice")
		}
		if err := value.Validate(); err != nil {
			return err
		}
		w.byte(byte(i))
		w.f32(value.DepthCents)
		w.f32(value.RateHz)
		w.f32(value.DelayMS)
	}
	return nil
}
func readVibrato(r *reader, cfg *engine.Config) error {
	count, err := r.byte()
	if err != nil || count == 0 || int(count) > cfg.Tracks {
		return imageError("invalid instrument vibrato count")
	}
	for range int(count) {
		index, err := r.byte()
		if err != nil || int(index) >= cfg.Tracks {
			return imageError("invalid instrument vibrato track")
		}
		track := &cfg.Track[index]
		if track.Vibrato != nil {
			return imageError("duplicate instrument vibrato track")
		}
		if track.Kind != engine.VoiceAcid && track.Kind != engine.VoiceGraph && track.Kind != engine.VoiceGraphPoly {
			return imageError("unsupported instrument vibrato voice")
		}
		value := &expression.VibratoConfig{}
		if value.DepthCents, err = r.f32(); err != nil {
			return err
		}
		if value.RateHz, err = r.f32(); err != nil {
			return err
		}
		if value.DelayMS, err = r.f32(); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return err
		}
		track.Vibrato = value
	}
	return nil
}
