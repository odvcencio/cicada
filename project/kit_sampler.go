package project

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
)

// KitSamplerLane is a drum lane that starts one note of a sampler pack.
type KitSamplerLane struct {
	Lane    drum.Lane
	Source  string // lane name as the kit spells it
	Sampler string // sampler name, as declared
	Note    uint8  // MIDI note; c4 is 60, d2 is 38
}

// ParseSamplerPitch reads a pitch name such as d2, c#2 or eb3 as a MIDI note.
// The octave follows the sampler root: c4 is 60.
func ParseSamplerPitch(name string) (int, bool) { return notation.ParseSamplerPitch(name) }

// isSamplerTarget reports whether a kit target is written as
// "<sampler>.<pitch>": the prefix names a declared sampler, or the suffix reads
// as a pitch so that an undeclared sampler gets a reference error.
func isSamplerTarget(target string, samplers map[string]bool) (sampler, pitch string, ok bool) {
	sampler, pitch, ok = strings.Cut(target, ".")
	if !ok || strings.Contains(pitch, ".") || sampler == "builtin" || sampler == "model" {
		return "", "", false
	}
	if _, declared := samplers[sampler]; declared {
		return sampler, pitch, true
	}
	_, shaped := ParseSamplerPitch(pitch)
	return sampler, pitch, shaped
}

// KitSamplerLanes resolves the lanes of a kit that name a sampler. samplers maps
// each declared sampler name to whether it is a sample pack. A target that names
// a compiled instrument is not a sampler lane. It refuses an unknown sampler, a
// sampler that is not a pack, and a pitch that is not a note name.
func KitSamplerLanes(kit Kit, programs map[string]*instrument.Program, samplers map[string]bool) ([]KitSamplerLane, error) {
	var lanes []KitSamplerLane
	for _, source := range sortedKeys(kit.Lanes) {
		target := kit.Lanes[source]
		if programs[target] != nil {
			continue
		}
		name, pitch, ok := isSamplerTarget(target, samplers)
		if !ok {
			continue
		}
		lane, ok := drumLane(source)
		if !ok {
			return nil, fmt.Errorf("kit %s has unknown lane %s", kit.ID, source)
		}
		isPack, declared := samplers[name]
		if !declared {
			return nil, fmt.Errorf("CICADA-REFERENCE: kit %s lane %s binds %s, but no sampler named %s is declared", kit.ID, source, target, name)
		}
		if !isPack {
			return nil, fmt.Errorf("CICADA-UNSUPPORTED: kit %s lane %s binds sampler %s, which is not a sample pack; kit lanes need pack = \"...\"", kit.ID, source, name)
		}
		note, ok := ParseSamplerPitch(pitch)
		if !ok {
			return nil, fmt.Errorf("CICADA-PARAM: kit %s lane %s: %q is not a note name from c0 to g9, such as d2 or c#2", kit.ID, source, pitch)
		}
		lanes = append(lanes, KitSamplerLane{Lane: lane, Source: source, Sampler: name, Note: uint8(note)})
	}
	return lanes, nil
}

// SamplerPackSet lists the declared samplers and whether each is a sample pack.
func (p *Project) SamplerPackSet() map[string]bool {
	set := make(map[string]bool, len(p.Samplers))
	for _, sampler := range p.Samplers {
		set[sampler.Name] = sampler.Pack != ""
	}
	return set
}
