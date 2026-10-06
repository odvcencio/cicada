package project

import "m31labs.dev/cicada/kernel/graph"

// GraphPolyphony preserves the eight-voice Live instrument mode and selects
// the four-voice cohort pool for instruments assigned chord patterns.
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
