package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"math"
)

// validateUnifiedFields checks the new image records symmetrically, without
// creating voices or changing a live engine. Index-based traversal avoids
// copying the large fixed pattern array into TinyGo initialization code. Engine construction remains the
// authority for the complete schedule/voice budget, as for legacy images.
func validateUnifiedFields(cfg *engine.Config) error {
	if math.IsNaN(float64(cfg.MasterBiasL)) || math.IsInf(float64(cfg.MasterBiasL), 0) || math.IsNaN(float64(cfg.MasterBiasR)) || math.IsInf(float64(cfg.MasterBiasR), 0) || math.IsNaN(cfg.MasterGainDB) || math.IsInf(cfg.MasterGainDB, 0) || cfg.MasterGainDB < -120 || cfg.MasterGainDB > 24 {
		return Error("invalid unified master settings")
	}
	if len(cfg.Schedule) > 65535 || len(cfg.Assets) > 65535 || len(cfg.Clips) > 65535 || len(cfg.Schedule) > 0 && len(cfg.Song) > 0 {
		return Error("invalid schedule image counts or authority")
	}
	for _, a := range cfg.Assets {
		if a.SampleRate < 8000 || a.SampleRate > 192000 || len(a.Left) == 0 || len(a.Right) > 0 && len(a.Right) != len(a.Left) {
			return Error("invalid audio asset dimensions")
		}
	}
	for _, c := range cfg.Clips {
		if int(c.Asset) >= len(cfg.Assets) || c.StartFrame < 0 || c.EndFrame <= c.StartFrame || c.EndFrame > int64(len(cfg.Assets[c.Asset].Left)) || c.FadeInFrames < 0 || c.FadeOutFrames < 0 || c.FadeInFrames > c.EndFrame-c.StartFrame || c.FadeOutFrames > c.EndFrame-c.StartFrame || math.IsNaN(c.GainDB) || math.IsInf(c.GainDB, 0) || c.GainDB < -60 || c.GainDB > 24 {
			return Error("invalid clip image region or asset")
		}
	}
	for track := 0; track < cfg.Tracks; track++ {
		spec := &cfg.Track[track]
		if spec.Polyphony != 0 && (spec.Polyphony != 4 || spec.Kind != engine.VoiceGraph) {
			return Error("invalid polyphony image mode")
		}
		if spec.Kind == engine.VoiceSample {
			if spec.Sample == nil || int(spec.Sample.Asset) >= len(cfg.Assets) || spec.Sample.RootKey > 127 || spec.Sample.Voices < 1 || spec.Sample.Voices > 32 {
				return Error("invalid sampler image configuration")
			}
		}
		if len(cfg.Patterns) == 0 {
			continue
		}
		for slot := range cfg.Patterns[track].Slots {
			p := &cfg.Patterns[track].Slots[slot]
			if p.Len == 0 {
				if p.Chords != [64]seq.ChordStep{} {
					return Error("unused slot contains chord payload")
				}
				continue
			}
			if err := p.Validate(); err != nil {
				return err
			}
			for _, chord := range p.Chords {
				if chord.Count > 0 && spec.Polyphony != 4 && spec.Kind != engine.VoicePiano {
					return Error("chord payload requires a polyphonic graph track")
				}
			}
		}
	}
	for _, s := range cfg.Scenes {
		for track, b := range s.Track {
			if b.Mode == engine.SceneClip && (track >= cfg.Tracks || cfg.Track[track].Kind != engine.VoiceAudio || int(b.Clip) >= len(cfg.Clips)) {
				return Error("invalid scene clip image binding")
			}
		}
	}
	var previous int64
	for i, v := range cfg.Schedule {
		if v.Tick < 0 || v.EndTick < v.Tick || v.EndTick > 1<<53-1 || i > 0 && v.Tick < previous || v.Kind > engine.ScheduleClipEnd {
			return Error("invalid schedule image event")
		}
		previous = v.Tick
		if v.Kind == engine.ScheduleScene {
			if int(v.Scene) >= len(cfg.Scenes) || v.EndTick <= v.Tick {
				return Error("invalid schedule scene image")
			}
			continue
		}
		if int(v.Track) >= cfg.Tracks || v.ID == 0 {
			return Error("invalid schedule image track or identity")
		}
		if v.Kind == engine.SchedulePattern && (v.Index >= 16 || len(cfg.Patterns) == 0 || cfg.Patterns[v.Track].Slots[v.Index].Len == 0 || cfg.Track[v.Track].Kind == engine.VoiceAudio || v.Tick%seq.TicksPerStep != 0 || v.EndTick <= v.Tick) {
			return Error("invalid schedule pattern image")
		}
		if v.Kind == engine.ScheduleClip && (int(v.Index) >= len(cfg.Clips) || cfg.Track[v.Track].Kind != engine.VoiceAudio || v.EndTick <= v.Tick) {
			return Error("invalid schedule clip image")
		}
	}
	return nil
}
