package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

func sharedPointCount(cfg *engine.Config) int {
	count := 0
	for track := 0; track < cfg.Tracks; track++ {
		bank := &cfg.Patterns[track]
		for slot := range bank.Slots {
			pattern := &bank.Slots[slot]
			if p := pattern.Performance; p != nil {
				for _, note := range p.Notes {
					count += len(note.Expressions)
				}
				for _, group := range p.Groups {
					count += len(group.Points)
				}
			}
		}
	}
	return count
}

func writeSharedGroups(w *writer, cfg *engine.Config) error {
	if sharedPointCount(cfg) > seq.MaxPerformanceExpressions {
		return imageError("too many shared gesture points")
	}
	count := 0
	for track := 0; track < cfg.Tracks; track++ {
		bank := &cfg.Patterns[track]
		for slot := range bank.Slots {
			pattern := &bank.Slots[slot]
			if pattern.Performance != nil && len(pattern.Performance.Groups) > 0 {
				count++
			}
		}
	}
	if count == 0 {
		return imageError("empty shared gesture table")
	}
	w.u16(uint16(count))
	for track := 0; track < cfg.Tracks; track++ {
		bank := &cfg.Patterns[track]
		for slot := range bank.Slots {
			pattern := &bank.Slots[slot]
			p := pattern.Performance
			if p == nil || len(p.Groups) == 0 {
				continue
			}
			if cfg.Track[track].Kind != engine.VoiceAcid && cfg.Track[track].Kind != engine.VoiceGraph && cfg.Track[track].Kind != engine.VoiceGraphPoly {
				return imageError("shared gestures need acid or an authored synth")
			}
			w.byte(byte(track))
			w.byte(byte(slot))
			w.u16(uint16(len(p.Groups)))
			for _, group := range p.Groups {
				w.byte(byte(len(group.Name)))
				w.data = append(w.data, group.Name...)
				w.u32(uint32(len(group.Points)))
				for _, point := range group.Points {
					w.u64(uint64(point.Tick))
					w.f32(point.Value.PitchCents)
					w.f32(point.Value.Pressure)
					w.f32(point.Value.Timbre)
				}
			}
			w.u16(uint16(len(p.Notes)))
			for _, note := range p.Notes {
				w.u16(note.Group)
			}
		}
	}
	return nil
}

func readSharedGroups(r *reader, cfg *engine.Config) error {
	count, err := r.u16()
	if err != nil || count == 0 || int(count) > cfg.Tracks*16 {
		return imageError("invalid shared gesture table")
	}
	remaining := seq.MaxPerformanceExpressions - sharedPointCount(cfg)
	for range count {
		track, err := r.byte()
		if err != nil {
			return err
		}
		slot, err := r.byte()
		if err != nil {
			return err
		}
		if int(track) >= cfg.Tracks || slot >= 16 || (cfg.Track[track].Kind != engine.VoiceAcid && cfg.Track[track].Kind != engine.VoiceGraph && cfg.Track[track].Kind != engine.VoiceGraphPoly) {
			return imageError("invalid shared gesture target")
		}
		p := cfg.Patterns[track].Slots[slot].Performance
		n, err := r.u16()
		if err != nil || p == nil || len(p.Groups) > 0 || n == 0 || n > seq.MaxPerformanceNotes {
			return imageError("invalid shared gesture count")
		}
		p.Groups = make([]seq.ControlGroup, int(n))
		for i := range p.Groups {
			group := &p.Groups[i]
			length, err := r.byte()
			if err != nil || length == 0 || length > 64 {
				return imageError("invalid shared gesture name")
			}
			name, err := r.take(int(length))
			if err != nil {
				return err
			}
			group.Name = string(name)
			n, err := r.u32()
			if err != nil || n == 0 || n > uint32(remaining) || int64(n)*20 > int64(len(r.data)-r.at) {
				return imageError("invalid shared gesture point count")
			}
			remaining -= int(n)
			group.Points = make([]seq.GroupPoint, int(n))
			for j := range group.Points {
				point := &group.Points[j]
				tick, err := r.u64()
				if err != nil || tick >= uint64(p.LengthTicks) {
					return imageError("invalid shared gesture tick")
				}
				point.Tick = int64(tick)
				for _, field := range []*float32{&point.Value.PitchCents, &point.Value.Pressure, &point.Value.Timbre} {
					*field, err = r.f32()
					if err != nil {
						return err
					}
				}
			}
		}
		notes, err := r.u16()
		if err != nil || int(notes) != len(p.Notes) {
			return imageError("invalid shared note binding count")
		}
		for i := range p.Notes {
			id, err := r.u16()
			if err != nil || int(id) > len(p.Groups) {
				return imageError("invalid shared note binding")
			}
			p.Notes[i].Group = id
		}
	}
	return validatePortablePerformances(cfg)
}
