package project

import "strings"

// RecordingPatternChoices takes a valid project and keeps current destinations first, then
// offers compatible, unused local patterns in library order. A pattern used
// only by another track is not an implicit destination for this track.
// Recording into a library pattern never assigns a scene or consumes a slot.
func RecordingPatternChoices(p *Project) map[string][]string {
	choices := map[string][]string{}
	if p == nil {
		return choices
	}
	used := map[string]bool{}
	for _, track := range p.Tracks {
		for _, slot := range track.Slots {
			if slot != nil {
				used[*slot] = true
			}
		}
	}
	for _, track := range p.Tracks {
		seen := map[string]bool{}
		for _, slot := range track.Slots {
			if slot != nil && !seen[*slot] {
				seen[*slot] = true
				choices[track.ID] = append(choices[track.ID], *slot)
			}
		}
		for _, pattern := range p.Patterns {
			// Qualified declarations belong to imported libraries. Keeping a
			// take must not offer to overwrite an unused imported pattern.
			if !used[pattern.ID] && !strings.Contains(pattern.ID, ".") && PatternTrackCompatible(p, pattern, track) {
				choices[track.ID] = append(choices[track.ID], pattern.ID)
			}
		}
	}
	return choices
}
