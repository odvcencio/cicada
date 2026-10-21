package edit

import (
	"fmt"
	"strings"
)

// Relative degrees keep starting ideas in the score's key and the instrument's
// home register. Degrees 1, 3, 5 and 7 exist in all eight supported scales.
// These are ordinary editable grids, with no generator state to recover.
func patternStarter(starter, kind string, lanes []string, steps int) (map[string]string, error) {
	switch starter {
	case "", "empty", "pulse":
	case "arpeggio":
		if kind == "drums" {
			return nil, fmt.Errorf("an arpeggio needs an instrument track; choose a pulse or backbeat for drums")
		}
	case "chords":
		if kind != "notes" {
			return nil, fmt.Errorf("held chords need a polyphonic instrument; choose a pulse or arpeggio for this sound")
		}
	case "backbeat":
		if kind != "drums" {
			return nil, fmt.Errorf("a backbeat needs a drum track; choose a pulse, arpeggio or held chords for an instrument")
		}
	default:
		return nil, fmt.Errorf("choose an empty grid, pulse, arpeggio, held chords or backbeat")
	}
	rows := make(map[string]string)
	if kind == "drums" {
		canonical := false
		for _, lane := range lanes {
			canonical = canonical || lane == "bd" || lane == "sd" || lane == "ch"
		}
		for index, lane := range lanes {
			var row strings.Builder
			for step := 0; step < steps; step++ {
				hit := starter == "pulse" && index == 0 && step%4 == 0
				if starter == "backbeat" {
					hit = lane == "bd" && step%8 == 0 || lane == "sd" && step%8 == 4 || lane == "ch" && step%2 == 0 || !canonical && index == 0 && step%4 == 0
				}
				if hit {
					row.WriteByte('x')
				} else {
					row.WriteByte('.')
				}
			}
			rows[lane] = row.String()
		}
		return rows, nil
	}
	tokens := make([]string, steps)
	arpeggio := [...]string{"1", "3", "5", "7", "1'", "7", "5", "3"}
	for step := range tokens {
		tokens[step] = "."
		switch starter {
		case "pulse":
			if step%4 == 0 {
				tokens[step] = "1"
			}
		case "arpeggio":
			if step%2 == 0 {
				tokens[step] = arpeggio[(step/2)%len(arpeggio)]
			}
		case "chords":
			tokens[step] = "-"
			if step%16 == 0 {
				tokens[step] = "[1 3 5]"
			}
			if step%16 == 8 {
				tokens[step] = "[3 5 7]"
			}
		}
	}
	rows[""] = strings.Join(tokens, " ")
	return rows, nil
}
