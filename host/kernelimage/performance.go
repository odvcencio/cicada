package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// This negotiated capability is outside the image header's legacy uint16 flags.
// Version 16 is otherwise the unchanged version-15 layout with an appended table.
const CapabilityPerformance uint32 = 1 << 18
const CapabilityGestures uint32 = 1 << 19

// The worklet drains at most 256 status records each eight 128-frame quanta.
// Reserve half that throughput for its other status and grid tracks. A burst
// fits the engine's pre-admitted storage, but an excessive sustained event rate
// must fail at load time, rather than eventually stopping audible playback.
func validatePortablePerformances(cfg *engine.Config) error {
	window := (int64(8*128)*300000*seq.PPQ + int64(60000*cfg.SampleRate) - 1) / int64(60000*cfg.SampleRate)
	var total int64
	controllerTotal := 0
	for track := range cfg.Patterns {
		var busiest int64
		controllers := 0
		for slot := range cfg.Patterns[track].Slots {
			if p := cfg.Patterns[track].Slots[slot].Performance; p != nil {
				if err := p.Validate(); err != nil {
					return err
				}
				busiest = max(busiest, (2*int64(len(p.Notes))*window+p.LengthTicks-1)/p.LengthTicks)
				controllers = max(controllers, p.PeakEvents(window+1, false))
			}
		}
		total += busiest
		controllerTotal += controllers
	}
	if total > 128 {
		return imageError("performance density exceeds portable status capacity; use native playback or a longer loop")
	}
	if controllerTotal > 256 {
		return imageError("performed controllers exceed event budget")
	}
	return nil
}

// Version 17 appends controller data after the complete version-16 payload.
// Older images retain their bytes and negotiate no new capability.
func writePerformedExpressions(w *writer, cfg *engine.Config) error {
	count, points := 0, 0
	for track := range cfg.Patterns {
		for slot := range cfg.Patterns[track].Slots {
			if p := cfg.Patterns[track].Slots[slot].Performance; p != nil {
				for _, note := range p.Notes {
					if len(note.Expressions) > 0 {
						if cfg.Track[track].Kind != engine.VoiceAcid && cfg.Track[track].Kind != engine.VoiceGraph {
							return imageError("gestures need acid or an authored synth")
						}
						count++
						points += len(note.Expressions)
					}
				}
			}
		}
	}
	if points > seq.MaxPerformanceExpressions || count == 0 {
		return imageError("invalid performed gesture table size")
	}
	w.u32(uint32(count))
	for track := range cfg.Patterns {
		for slot := range cfg.Patterns[track].Slots {
			if p := cfg.Patterns[track].Slots[slot].Performance; p != nil {
				for index, note := range p.Notes {
					if len(note.Expressions) == 0 {
						continue
					}
					w.byte(byte(track))
					w.byte(byte(slot))
					w.u16(uint16(index))
					w.u32(uint32(len(note.Expressions)))
					for _, point := range note.Expressions {
						w.u64(uint64(point.Tick))
						p := point.Params
						w.f32(p.PitchCents)
						w.f32(p.Pressure)
						w.f32(p.Timbre)
						w.f32(p.VibratoRateHz)
						w.f32(p.VibratoDepthCents)
					}
				}
			}
		}
	}
	return nil
}

func readPerformedExpressions(r *reader, cfg *engine.Config) error {
	count, err := r.u32()
	if err != nil || count == 0 || count > seq.MaxPerformanceExpressions {
		return imageError("invalid gesture data")
	}
	remaining := seq.MaxPerformanceExpressions
	for entry := uint32(0); entry < count; entry++ {
		track, err := r.byte()
		if err != nil {
			return err
		}
		slot, err := r.byte()
		if err != nil {
			return err
		}
		index, err := r.u16()
		if err != nil {
			return err
		}
		if int(track) >= cfg.Tracks || slot >= 16 || (cfg.Track[track].Kind != engine.VoiceAcid && cfg.Track[track].Kind != engine.VoiceGraph) {
			return imageError("invalid gesture data")
		}
		performance := cfg.Patterns[track].Slots[slot].Performance
		if performance == nil || int(index) >= len(performance.Notes) || performance.Notes[index].Expressions != nil {
			return imageError("invalid gesture data")
		}
		n, err := r.u32()
		if err != nil || n == 0 || n > uint32(remaining) || int64(n)*28 > int64(len(r.data)-r.at) {
			return imageError("invalid gesture data")
		}
		remaining -= int(n)
		note := &performance.Notes[index]
		note.Expressions = make([]seq.PerformedExpression, int(n))
		for i := range note.Expressions {
			point := &note.Expressions[i]
			tick, err := r.u64()
			if err != nil || tick > uint64(note.EndTick-note.Tick) {
				return imageError("invalid gesture data")
			}
			point.Tick, point.Params.Set = int64(tick), true
			for _, field := range []*float32{&point.Params.PitchCents, &point.Params.Pressure, &point.Params.Timbre, &point.Params.VibratoRateHz, &point.Params.VibratoDepthCents} {
				*field, err = r.f32()
				if err != nil {
					return err
				}
			}
		}
	}
	return validatePortablePerformances(cfg)
}

func writePerformances(w *writer, cfg *engine.Config) error {
	if err := validatePortablePerformances(cfg); err != nil {
		return err
	}
	count := 0
	for track := range cfg.Patterns {
		for slot := range cfg.Patterns[track].Slots {
			if cfg.Patterns[track].Slots[slot].Performance != nil {
				count++
			}
		}
	}
	w.u16(uint16(count))
	for track := range cfg.Patterns {
		for slot := range cfg.Patterns[track].Slots {
			p := cfg.Patterns[track].Slots[slot].Performance
			if p == nil {
				continue
			}
			if err := p.Validate(); err != nil {
				return err
			}
			w.byte(byte(track))
			w.byte(byte(slot))
			w.u64(uint64(p.LengthTicks))
			w.u16(uint16(len(p.Notes)))
			for _, note := range p.Notes {
				w.u64(uint64(note.Tick))
				w.u64(uint64(note.EndTick))
				w.byte(note.Note)
				w.byte(note.Velocity)
			}
		}
	}
	return nil
}

func readPerformances(r *reader, cfg *engine.Config) error {
	count, err := r.u16()
	if err != nil || int(count) > cfg.Tracks*16 {
		return imageError("invalid performance table count")
	}
	for i := 0; i < int(count); i++ {
		track, err := r.byte()
		if err != nil {
			return err
		}
		slot, err := r.byte()
		if err != nil || int(track) >= cfg.Tracks || slot >= 16 {
			return imageError("invalid performance pattern target")
		}
		target := &cfg.Patterns[track].Slots[slot]
		if target.Performance != nil || target.Len != 1 {
			return imageError("duplicate or empty performance target")
		}
		length, err := r.u64()
		if err != nil || length < 30 || length > 1<<40 {
			return imageError("invalid performance length")
		}
		notes, err := r.u16()
		if err != nil || notes < 1 || notes > seq.MaxPerformanceNotes {
			return imageError("invalid performed note count")
		}
		p := &seq.Performance{LengthTicks: int64(length), Notes: make([]seq.PerformedNote, int(notes))}
		for n := range p.Notes {
			at, err := r.u64()
			if err != nil || at > length {
				return imageError("invalid performed note start")
			}
			end, err := r.u64()
			if err != nil || end > length {
				return imageError("invalid performed note end")
			}
			pitch, err := r.byte()
			if err != nil {
				return err
			}
			velocity, err := r.byte()
			if err != nil {
				return err
			}
			p.Notes[n] = seq.PerformedNote{Tick: int64(at), EndTick: int64(end), Note: pitch, Velocity: velocity}
		}
		if err := p.Validate(); err != nil {
			return err
		}
		target.Performance = p
	}
	return validatePortablePerformances(cfg)
}
