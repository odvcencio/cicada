package recording

import (
	"fmt"
	"strconv"
	"strings"
)

// grooveCell is one sixteenth-note step: a rest when note is zero.
type grooveCell struct{ note, velocity int }

// kitGroove returns the starter's steps. A sampler track plays one note per
// step, so the groove never asks for two pieces at once. A kit with a kick, a
// snare and a hi-hat gets a two-bar rock groove that ends bar one on an open
// hat for the closed hat to cut. Any other kit gets one hit of each of its
// first sixteen pieces, a beat apart.
func kitGroove(recipe KitRecipe) []grooveCell {
	has := func(notes ...int) int {
		for _, want := range notes {
			for _, piece := range recipe.Pieces {
				if piece.Note == want {
					return want
				}
			}
		}
		return 0
	}
	kick, snare, hat, open := has(36, 35), has(38, 40, 37), has(42, 44), has(46)
	if kick == 0 || snare == 0 || hat == 0 {
		var cells []grooveCell
		for i, piece := range recipe.Pieces {
			if i == 16 {
				break
			}
			cells = append(cells, grooveCell{piece.Note, 100}, grooveCell{}, grooveCell{}, grooveCell{})
		}
		return cells
	}
	if open == 0 {
		open = hat
	}
	return []grooveCell{
		{kick, 112}, {}, {hat, 72}, {}, {snare, 104}, {}, {hat, 60}, {},
		{kick, 100}, {}, {hat, 72}, {}, {snare, 108}, {snare, 34}, {open, 84}, {},
		{kick, 112}, {}, {hat, 72}, {}, {snare, 104}, {kick, 90}, {hat, 60}, {},
		{kick, 100}, {}, {hat, 72}, {}, {snare, 108}, {}, {hat, 70}, {hat, 40},
	}
}

// kitStarter returns a score that plays the kit on one sampler track. Pieces
// are played by note number: c2 is the note 36, a kick in General MIDI.
func kitStarter(p *Pack, recipe KitRecipe) string {
	cells := kitGroove(recipe)
	steps := make([]string, len(cells))
	velocity := make([]string, len(cells))
	explicit := false
	for i, c := range cells {
		if c.note == 0 {
			steps[i], velocity[i] = ".", "."
			if i == 0 {
				velocity[i] = "100"
			}
			continue
		}
		steps[i], velocity[i] = NoteName(c.note), strconv.Itoa(c.velocity)
		explicit = explicit || c.velocity != 100
	}
	var pattern strings.Builder
	pattern.WriteString("pattern groove notes {\n")
	for from := 0; from < len(steps); from += 16 {
		fmt.Fprintf(&pattern, "  %s\n", strings.Join(steps[from:min(len(steps), from+16)], " "))
	}
	if explicit {
		fmt.Fprintf(&pattern, "  velocity: %s\n", strings.Join(velocity, " "))
	}
	pattern.WriteString("}\n")
	return fmt.Sprintf("cicada 2\ntempo 100\n%s\ntrack drums %s { level = -3dB }\n%sscene main { drums = groove }\nsong { main*4 }\n", p.Source("manifest.json", recipe.Pieces[0].Note), p.Manifest.ID, pattern.String())
}
