package project

import "m31labs.dev/cicada/kernel/graph"

// GraphPolyphony preserves the eight-voice Live instrument mode and selects
// the four-voice cohort pool for instruments assigned chord patterns or
// performed gestures, which need independent note ownership.
func GraphPolyphony(p *Project, kind string) int {
	for _, track := range p.Tracks {
		if track.Kind != kind {
			continue
		}
		for _, slot := range track.Slots {
			if slot == nil {
				continue
			}
			for _, pattern := range p.Patterns {
				if pattern.ID != *slot {
					continue
				}
				if pattern.Performance != nil {
					for _, note := range pattern.Performance.Notes {
						if len(note.Expressions) > 0 && len(pattern.Performance.Groups) == 0 {
							return graph.MaxPolyphony
						}
					}
				}
				for _, step := range pattern.Data {
					if step != nil && len(step.Notes) > 1 {
						return graph.MaxPolyphony
					}
				}
			}
		}
	}
	return graph.PolyVoices
}
