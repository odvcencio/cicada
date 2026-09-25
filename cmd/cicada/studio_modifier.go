package main

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/notation"
)

// toggledModifierSource changes the authored note token, including when that
// token belongs to a phrase used by multiple pattern steps.
func toggledModifierSource(source []byte, patternID, laneID string, index int, modifier string) ([]byte, error) {
	if patternID == "" || laneID != "" || index < 0 || modifier != "accent" && modifier != "slide" {
		return nil, fmt.Errorf("a note pattern, nonnegative step, and accent or slide are required")
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
			return nil, fmt.Errorf("step %d needs a note before setting %s", index+1, modifier)
		}
		start := studioSourceOffset(source, token.Position)
		end := start + len(token.Text)
		if end > len(source) || string(source[start:end]) != token.Text {
			return nil, fmt.Errorf("step source no longer matches the projection")
		}
		mark, before := "^", "~*?%"
		if modifier == "slide" {
			mark, before = "~", "*?%"
		}
		replacement := token.Text
		if strings.Contains(replacement, mark) {
			replacement = strings.ReplaceAll(replacement, mark, "")
		} else if at := strings.IndexAny(replacement, before); at >= 0 {
			replacement = replacement[:at] + mark + replacement[at:]
		} else {
			replacement += mark
		}
		updated := make([]byte, 0, len(source)-len(token.Text)+len(replacement))
		updated = append(updated, source[:start]...)
		updated = append(updated, replacement...)
		updated = append(updated, source[end:]...)
		return updated, nil
	}
	return nil, fmt.Errorf("unknown pattern %q", patternID)
}
