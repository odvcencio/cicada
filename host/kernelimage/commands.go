package kernelimage

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

const CapabilityChords uint32 = 1

// PatternCommands uploads one melodic slot through the unchanged 24-byte ABI.
// A chord requires an advertised capability; no mono fallback is permitted.
// cfg describes the currently loaded kernel, not the source project. Gate and
// seed must match its preloaded slot (or blank defaults). Submit the returned
// commands in one batch before selecting/playing the slot.
func PatternCommands(pattern seq.Pattern, cfg *engine.Config, track, slot uint8, capabilities uint32) ([]cmd.Command, error) {
	if cfg == nil || cfg.Tracks < 1 || cfg.Tracks > 16 || int(track) >= cfg.Tracks || slot >= 16 || len(cfg.Patterns) != 0 && len(cfg.Patterns) != cfg.Tracks || cfg.Track[track].Kind != engine.VoiceAcid && cfg.Track[track].Kind != engine.VoiceGraph {
		return nil, Error("invalid melodic pattern upload target")
	}
	gate, seed := uint8(55), cfg.Seed
	if len(cfg.Patterns) > 0 {
		loaded := cfg.Patterns[track].Slots[slot]
		if loaded.Len > 0 {
			gate, seed = loaded.GatePercent, loaded.Seed
		}
	}
	if pattern.GatePercent != gate || pattern.Seed != seed {
		return nil, Error("command upload requires matching preloaded gate and seed metadata; use a complete project image")
	}
	if err := pattern.Validate(); err != nil {
		return nil, err
	}
	for _, chord := range pattern.Chords {
		if chord.Count > 0 && (capabilities&CapabilityChords == 0 || cfg.Track[track].Polyphony != 4) {
			return nil, Error("target kernel does not support polyphonic chord uploads")
		}
	}
	commands := make([]cmd.Command, 0, 66+int(pattern.Len)*2)
	// Clear the loaded slot inside this same batch before setting length/meta.
	// This avoids invalid intermediate chord/slide/transposition combinations.
	if len(cfg.Patterns) > 0 {
		loaded := cfg.Patterns[track].Slots[slot]
		if loaded.Len > 0 {
			if err := loaded.Validate(); err != nil {
				return nil, err
			}
			rest, _ := seq.PackStep(seq.Step{Ratchet: 1, Probability: 100})
			for i := uint8(0); i < loaded.Len; i++ {
				commands = append(commands, cmd.Command{Op: cmd.OpSetStep, Track: track, Index: uint16(i), Arg0: rest, Arg1: uint32(slot)})
			}
		}
	}
	commands = append(commands, cmd.Command{Op: cmd.OpSetPatternLen, Track: track, Index: uint16(pattern.Len), Arg1: uint32(slot)}, cmd.Command{Op: cmd.OpSetPatternMeta, Track: track, Arg0: uint32(pattern.SwingPermille) | uint32(uint16(int16(pattern.Transpose)))<<16, Arg1: uint32(slot)})
	for i := uint8(0); i < pattern.Len; i++ {
		commands = append(commands, cmd.Command{Op: cmd.OpSetStep, Track: track, Index: uint16(i), Arg0: pattern.Steps[i], Arg1: uint32(slot)})
		chord := pattern.Chords[i]
		if chord.Count > 0 {
			c, err := cmd.ChordStepCommand(track, slot, i, chord.Notes, chord.Count)
			if err != nil {
				return nil, err
			}
			commands = append(commands, c)
		}
	}
	return commands, nil
}
