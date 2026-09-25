package main

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

// cycledStepSource writes a musician-facing preset into the authored note.
// A phrase expansion points to the shared phrase token, as other grid edits do.
func cycledStepSource(source []byte, patternID, laneID string, index int, kind string) ([]byte, error) {
	if patternID == "" || laneID != "" || index < 0 || kind != "ratchet" && kind != "chance" {
		return nil, fmt.Errorf("a note pattern, nonnegative step, and ratchet or chance are required")
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return nil, fmt.Errorf("score must validate before a grid edit")
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != patternID {
			continue
		}
		if pattern.Kind == "drums" || index >= len(pattern.Steps) {
			return nil, fmt.Errorf("pattern %s has no note step %d", patternID, index+1)
		}
		token := pattern.Steps[index]
		if token.Text == "." || token.Text == "-" {
			return nil, fmt.Errorf("step %d needs a note before changing %s", index+1, kind)
		}
		start := studioSourceOffset(source, token.Position)
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		value := 1
		at := strings.IndexByte(token.Text, '*')
		if kind == "chance" {
			value, at = 100, strings.IndexAny(token.Text, "?%")
		}
		replacement := token.Text
		if at >= 0 {
			last := at + 1
			for last < len(replacement) && replacement[last] >= '0' && replacement[last] <= '9' {
				last++
			}
			parsed, err := strconv.Atoi(replacement[at+1 : last])
			if err != nil {
				return nil, fmt.Errorf("invalid %s on step %d: %w", kind, index+1, err)
			}
			value = parsed
			replacement = replacement[:at] + replacement[last:]
		}
		if kind == "ratchet" {
			if value < 1 || value > 8 {
				return nil, fmt.Errorf("ratchet on step %d is outside 1–8", index+1)
			}
			next := value%8 + 1
			if next > 1 {
				mark := "*" + strconv.Itoa(next)
				if before := strings.IndexAny(replacement, "?%"); before >= 0 {
					replacement = replacement[:before] + mark + replacement[before:]
				} else {
					replacement += mark
				}
			}
		} else {
			if value < 1 || value > 100 {
				return nil, fmt.Errorf("chance on step %d is outside 1–100", index+1)
			}
			switch {
			case value > 75:
				replacement += "?75"
			case value > 50:
				replacement += "?50"
			case value > 25:
				replacement += "?25"
			}
		}
		updated := make([]byte, 0, len(source)-len(token.Text)+len(replacement))
		updated = append(updated, source[:start]...)
		updated = append(updated, replacement...)
		updated = append(updated, source[end:]...)
		return updated, nil
	}
	return nil, fmt.Errorf("unknown pattern %q", patternID)
}
