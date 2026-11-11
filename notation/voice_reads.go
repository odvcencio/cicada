package notation

import (
	"strings"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// The per-step rows and marks a melodic pattern can carry.
const (
	InputAccent   = "accent"   // a step's ^
	InputSlide    = "slide"    // a step's ~
	InputRatchet  = "ratchet"  // a step's *N
	InputChance   = "chance"   // a step's ?N
	InputVelocity = "velocity" // the velocity: row
	InputPressure = "pressure" // the pressure: row
	InputTimbre   = "timbre"   // the timbre: row
	InputBend     = "bend"     // the bend: row
	InputVibrato  = "vibrato"  // the vibrato: row
)

// StepInputs lists the rows and marks in the order messages name them.
var StepInputs = []string{InputAccent, InputSlide, InputRatchet, InputChance, InputVelocity, InputPressure, InputTimbre, InputBend, InputVibrato}

// VoiceReads describes which per-step rows and marks of a melodic pattern a
// voice plays. A true entry changes what the voice plays; a false entry is
// accepted and has no effect; an absent entry is refused by validation when
// the voice plays a pattern that carries it. A nil result means the voice is
// not a melodic voice (drums, a kit, audio) or the score does not declare it.
//
// The engine applies slides, ratchets and chance to every voice. The acid
// voice reads accent, bend and vibrato and ignores velocity, pressure and
// timbre. The piano and keyboard voices read velocity and refuse the
// expression rows. Samplers read velocity, ignore accent, pressure, bend and
// vibrato, and refuse timbre. Modal voices and guitar read velocity and
// ignore accent and the expression rows. An authored instrument reads
// velocity, pressure and timbre when its voice graph names them, and bend and
// vibrato when it is mono. The render table in render/voice_reads_test.go
// checks every entry against the engine's output.
func VoiceReads(s *Score, kind string) map[string]bool {
	reads := map[string]bool{InputSlide: true, InputRatchet: true, InputChance: true}
	set := func(entries map[string]bool) map[string]bool {
		for input, read := range entries {
			reads[input] = read
		}
		return reads
	}
	kind = ResolveVoice(s, kind)
	_, isModal := modal.ParseTrackKind(kind)
	switch {
	case kind == "acid":
		return set(map[string]bool{InputAccent: true, InputVelocity: false, InputPressure: false, InputTimbre: false, InputBend: true, InputVibrato: true})
	case kind == "piano" || keyboard.ID(kind) != 0 && !declaresInstrument(s, kind):
		return set(map[string]bool{InputAccent: false, InputVelocity: true})
	case isModal || kind == "guitar":
		return set(map[string]bool{InputAccent: false, InputVelocity: true, InputPressure: false, InputTimbre: false, InputBend: false, InputVibrato: false})
	}
	for _, sampler := range s.Samplers {
		if sampler.Name == kind {
			return set(map[string]bool{InputAccent: false, InputVelocity: true, InputPressure: false, InputBend: false, InputVibrato: false})
		}
	}
	for _, instrument := range s.Instruments {
		if instrument.Name != kind {
			continue
		}
		names := instrumentNames(instrument)
		mono := instrument.Mode != "poly"
		return set(map[string]bool{InputAccent: false, InputVelocity: names["velocity"], InputPressure: names["pressure"], InputTimbre: names["timbre"], InputBend: mono, InputVibrato: mono})
	}
	return nil
}

// ResolveVoice reads a track kind as the voice the engine binds: a preset as
// its target, and an import alias's name (tone.glass) as the library's
// declaration (demo.tone.glass).
func ResolveVoice(s *Score, kind string) string {
	for range 4 {
		resolved := kind
		if first, rest, ok := strings.Cut(kind, "."); ok {
			if namespace, found := s.LibraryAliases[first]; found {
				resolved = namespace + "." + rest
			}
		}
		for _, preset := range s.Presets {
			if (preset.Name == kind || preset.Name == resolved) && preset.Target != "" {
				resolved = preset.Target
			}
		}
		if resolved == kind {
			return kind
		}
		kind = resolved
	}
	return kind
}

// VoiceLanes are the drum lanes a voice plays: every built-in lane for drums,
// a kit's bound lanes (a library's kit included), and nil for a voice that is
// not a drum voice or that the score does not declare.
func VoiceLanes(s *Score, kind string) []string {
	kind = ResolveVoice(s, kind)
	if kind == "drums" {
		return append([]string(nil), drum.Names[:]...)
	}
	for _, kit := range s.Kits {
		if kit.Name == kind {
			lanes := make([]string, 0, len(kit.Bindings))
			for _, binding := range kit.Bindings {
				lanes = append(lanes, binding.Lane)
			}
			return lanes
		}
	}
	return nil
}

// PatternInputs lists the rows and marks a melodic pattern carries.
func PatternInputs(p Pattern) []string {
	carried := map[string]bool{}
	for _, step := range p.Steps {
		text := step.Text
		carried[InputAccent] = carried[InputAccent] || strings.Contains(text, "^")
		carried[InputSlide] = carried[InputSlide] || strings.Contains(text, "~")
		carried[InputRatchet] = carried[InputRatchet] || strings.Contains(text, "*")
		carried[InputChance] = carried[InputChance] || strings.Contains(text, "?")
	}
	carried[InputVelocity] = len(p.Velocity) > 0
	for _, row := range p.Expression {
		carried[row.Name] = true
	}
	var inputs []string
	for _, input := range StepInputs {
		if carried[input] {
			inputs = append(inputs, input)
		}
	}
	return inputs
}

func declaresInstrument(s *Score, name string) bool {
	for _, instrument := range s.Instruments {
		if instrument.Name == name {
			return true
		}
	}
	return false
}

// instrumentNames lists the names an instrument's voice graph reads.
func instrumentNames(instrument Instrument) map[string]bool {
	names := map[string]bool{}
	var walk func(*Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "name" {
			names[e.Text] = true
		}
		walk(e.Left)
		walk(e.Right)
		for _, arg := range e.Args {
			walk(arg)
		}
	}
	for _, let := range instrument.Lets {
		walk(let.Value)
	}
	walk(instrument.Output)
	return names
}
