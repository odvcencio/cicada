package project

import (
	"fmt"
	"m31labs.dev/cicada/notation"
)

func expandTuplets(source notation.Pattern) (notation.Pattern, uint16, error) {
	grid := uint16(240)
	for _, attr := range source.Attrs {
		if attr.Name == "step" {
			var err error
			grid, err = notation.GridTicks(attr.Value)
			if err != nil {
				return source, 0, err
			}
		}
	}
	subdivision := int(grid)
	grouped := false
	for _, step := range source.Steps {
		if len(step.ChordPitches) == 0 {
			continue
		}
		grouped = true
		count := len(step.ChordPitches)
		if count < 2 || count > 8 || int(grid)%count != 0 || step.ChordModifiers != "" {
			return source, 0, &patternCompileError{position: step.Position, err: fmt.Errorf("CICADA-GRID: tuplet groups need 2..8 pitches dividing a cell into whole ticks, without group modifiers")}
		}
		a, b := subdivision, int(grid)/count
		for b != 0 {
			a, b = b, a%b
		}
		subdivision = a
	}
	if !grouped {
		return source, 0, nil
	}
	if subdivision < 30 {
		return source, 0, fmt.Errorf("CICADA-GRID: expanded tuplet cells must be at least 30 ticks")
	}
	var steps []notation.StepToken
	for _, step := range source.Steps {
		cells := []notation.StepToken{step}
		if len(step.ChordPitches) > 0 {
			cells = nil
			for _, pitch := range step.ChordPitches {
				cells = append(cells, notation.StepToken{Text: pitch.Text, Position: step.Position, Transpose: step.Transpose})
			}
		}
		width := int(grid) / len(cells) / subdivision
		for _, cell := range cells {
			steps = append(steps, cell)
			for i := 1; i < width; i++ {
				tail := cell
				tail.Text = "-"
				if cell.Text == "." {
					tail.Text = "."
				}
				steps = append(steps, tail)
			}
		}
	}
	if len(steps) > 64 {
		return source, 0, fmt.Errorf("CICADA-LIMIT: tuplets expand beyond 64 cells")
	}
	source.Steps = steps
	return source, uint16(subdivision), nil
}
