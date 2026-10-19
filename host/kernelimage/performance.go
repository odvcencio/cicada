package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// This negotiated capability is outside the image header's legacy uint16 flags.
// Version 16 is otherwise the unchanged version-15 layout with an appended table.
const CapabilityPerformance uint32 = 1 << 18

// The worklet drains at most 256 status records each eight 128-frame quanta.
// Reserve half that throughput for its other status and grid tracks. A burst
// fits the engine's pre-admitted storage, but an excessive sustained event rate
// must fail at load time, rather than eventually stopping audible playback.
func validatePortablePerformances(cfg *engine.Config) error {
	window := (int64(8*128)*300000*seq.PPQ + int64(60000*cfg.SampleRate) - 1) / int64(60000*cfg.SampleRate)
	var total int64
	for track := range cfg.Patterns {
		var busiest int64
		for slot := range cfg.Patterns[track].Slots {
			if p := cfg.Patterns[track].Slots[slot].Performance; p != nil {
				if err := p.Validate(); err != nil {
					return err
				}
				busiest = max(busiest, (2*int64(len(p.Notes))*window+p.LengthTicks-1)/p.LengthTicks)
			}
		}
		total += busiest
	}
	if total > 128 {
		return Error("performance density exceeds portable status capacity; use native playback or a longer loop")
	}
	return nil
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
		return Error("invalid performance table count")
	}
	for i := 0; i < int(count); i++ {
		track, err := r.byte()
		if err != nil {
			return err
		}
		slot, err := r.byte()
		if err != nil || int(track) >= cfg.Tracks || slot >= 16 {
			return Error("invalid performance pattern target")
		}
		target := &cfg.Patterns[track].Slots[slot]
		if target.Performance != nil || target.Len != 1 {
			return Error("duplicate or empty performance target")
		}
		length, err := r.u64()
		if err != nil || length < 30 || length > 1<<40 {
			return Error("invalid performance length")
		}
		notes, err := r.u16()
		if err != nil || notes < 1 || notes > seq.MaxPerformanceNotes {
			return Error("invalid performed note count")
		}
		p := &seq.Performance{LengthTicks: int64(length), Notes: make([]seq.PerformedNote, int(notes))}
		for n := range p.Notes {
			at, err := r.u64()
			if err != nil || at > length {
				return Error("invalid performed note start")
			}
			end, err := r.u64()
			if err != nil || end > length {
				return Error("invalid performed note end")
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
