package project

import "fmt"

// ClipSlots derives a stable native launch bank in declaration order. Older
// edition-2 JSON projects can omit the audio bank; scene bindings suffice.
func ClipSlots(p *Project, track Track) ([16]*string, error) {
	var slots [16]*string
	used := map[string]bool{}
	for _, scene := range p.Scenes {
		id := scene.Bindings[track.ID]
		if id != "" && id != "off" && id != "keep" {
			used[id] = true
		}
	}
	next := 0
	for _, clip := range p.Clips {
		if !used[clip.Name] {
			continue
		}
		if next == len(slots) {
			return slots, fmt.Errorf("audio track %s uses more than 16 clips", track.ID)
		}
		id := clip.Name
		slots[next] = &id
		next++
	}
	return slots, nil
}
