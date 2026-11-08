package main

import (
	"fmt"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// recordedVelocities is the lowest and highest velocity at which an attack zone
// plays note, matching the range a kit lane clamps its hits to. The caller has
// checked that some attack zone covers note.
func recordedVelocities(zones instrumentpack.ZoneMap, note uint8) (low, high uint8) {
	low, high = 127, 1
	for _, zone := range zones {
		if !zone.Release && note >= zone.KeyLow && note <= zone.KeyHigh {
			low, high = min(low, max(zone.VelocityLow, 1)), max(high, zone.VelocityHigh)
		}
	}
	return low, high
}

// laneGap returns a velocity that a kit lane hit can play but no attack zone
// of note covers, or 0 when there is none. The engine humanizes a hit, plays an
// accent at 127, then clamps the velocity into the note's recorded range
// [low, high], so a gap can only sit inside that range.
func laneGap(zones instrumentpack.ZoneMap, note, velocity, humanize uint8, accent bool, low, high uint8) uint8 {
	from, to := max(1, int(velocity)-int(humanize)), min(127, int(velocity)+int(humanize))
	if accent {
		from, to = 127, 127
	}
	for v := from; v <= to; v++ {
		if c := min(max(uint8(v), low), high); !zones.HasAttackZone(note, c) {
			return c
		}
	}
	return 0
}

// kitSamplerZoneDiagnostics reports the drum lanes of an authored kit that bind
// a pack note the pack cannot play. A lane is reported at its kit binding when
// no attack zone covers its note at any velocity, and at the hit when the note
// has zones but none at a velocity the pattern plays on a track of that kit.
// The engine refuses a lane whose note has no zone, and plays a hit below or
// above the note's recorded velocities at the nearest one, so that is a
// warning. A velocity inside that range that no zone covers, written or
// reached under humanize, faults the kit when it plays, so that is an error.
func kitSamplerZoneDiagnostics(p *project.Project, score *notation.Score, packs map[string]admittedPack) []notation.Diagnostic {
	var diagnostics []notation.Diagnostic
	patterns := map[string]project.Pattern{}
	for _, pattern := range p.Patterns {
		patterns[pattern.ID] = pattern
	}
	// A lane that names an instrument is not a sampler lane, even when the
	// instrument's name reads like a pitch (demo.tone.e2). Only the names matter.
	programs := map[string]*instrument.Program{}
	for _, inst := range p.Instruments {
		programs[inst.ID] = &instrument.Program{}
	}
	sources := map[string]notation.Pattern{}
	for _, pattern := range score.Patterns {
		sources[pattern.Name] = pattern
	}
	for _, source := range score.Kits {
		kit := project.Kit{ID: source.Name, Lanes: map[string]string{}}
		positions := map[string]notation.Position{}
		for _, binding := range source.Bindings {
			kit.Lanes[binding.Lane] = binding.Target
			positions[binding.Lane] = binding.Position
		}
		lanes, err := project.KitSamplerLanes(kit, programs, p.SamplerPackSet())
		if err != nil {
			continue // validation already reported it
		}
		for _, lane := range lanes {
			admitted, ok := packs[lane.Sampler]
			if !ok {
				continue // the pack did not load; that is reported on its own
			}
			name := lane.Source
			covered := false
			for _, zone := range admitted.zones {
				if !zone.Release && lane.Note >= zone.KeyLow && lane.Note <= zone.KeyHigh {
					covered = true
					break
				}
			}
			if !covered {
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: samplerZoneCode, Severity: "error", Position: positions[name],
					Message: fmt.Sprintf("kit %s lane %s binds %s (MIDI %d), which is outside every zone of the pack for sampler %s (%s, which covers MIDI %s)",
						kit.ID, name, kit.Lanes[name], lane.Note, lane.Sampler, admitted.manifest, attackKeyRanges(admitted.zones)),
				})
				continue
			}
			for _, track := range p.Tracks {
				if track.Kind != kit.ID {
					continue
				}
				for _, id := range track.Slots {
					if id == nil {
						continue
					}
					pattern := patterns[*id]
					written := sources[*id]
					low, high := recordedVelocities(admitted.zones, lane.Note)
					for i, step := range pattern.Lanes[name] {
						if step == nil || step.Tie || step.Velocity == 0 {
							continue
						}
						clamped := min(max(step.Velocity, low), high)
						gap := laneGap(admitted.zones, lane.Note, step.Velocity, pattern.Humanize, step.Accent, low, high)
						if gap == 0 && (clamped == step.Velocity || step.Accent) {
							continue
						}
						position := written.Position
						for _, row := range written.Lanes {
							if row.Name == name && i < len(row.Hits) {
								position = row.Hits[i].Position
							}
						}
						if gap == 0 {
							diagnostics = append(diagnostics, notation.Diagnostic{
								Code: samplerZoneCode, Severity: "warning", Position: position,
								Message: fmt.Sprintf("lane %s of pattern %s plays %s (MIDI %d) at velocity %d on track %s, but the pack for sampler %s records that note only at velocities %d to %d, so it plays at %d",
									name, *id, kit.Lanes[name], lane.Note, step.Velocity, track.ID, lane.Sampler, low, high, clamped),
							})
							continue
						}
						diagnostics = append(diagnostics, notation.Diagnostic{
							Code: samplerZoneCode, Severity: "error", Position: position,
							Message: fmt.Sprintf("lane %s of pattern %s plays %s (MIDI %d) at velocity %d on track %s, but no zone of the pack for sampler %s covers velocity %d",
								name, *id, kit.Lanes[name], lane.Note, step.Velocity, track.ID, lane.Sampler, gap),
						})
					}
				}
			}
		}
	}
	return diagnostics
}
