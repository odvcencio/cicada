package project

import (
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

// Check compiles every declared instrument and every track/pattern pair used
// by a scene. It catches failures that syntax and reference checks cannot see.
func Check(score *notation.Score) (map[string]*instrument.Program, []notation.Diagnostic) {
	programs := make(map[string]*instrument.Program)
	if score == nil {
		return programs, nil
	}
	var diagnostics []notation.Diagnostic
	for _, definition := range score.Instruments {
		program, ds := instrument.Compile(definition)
		diagnostics = append(diagnostics, ds...)
		if program != nil {
			programs[definition.Name] = program
		}
	}
	tracks := make(map[string]notation.Track, len(score.Tracks))
	for _, track := range score.Tracks {
		tracks[track.Name] = track
		program := programs[track.Kind]
		if program == nil {
			continue
		}
		overrides := make(map[string]string, len(track.Params))
		for _, param := range track.Params {
			overrides[param.Name] = param.Value
		}
		if _, err := instrument.Lower(program, overrides); err != nil {
			diagnostics = append(diagnostics, notation.Diagnostic{
				Code: "CICADA-UNIT", Severity: "error", Message: err.Error(), Position: track.Position,
			})
		}
	}
	patterns := make(map[string]notation.Pattern, len(score.Patterns))
	for _, pattern := range score.Patterns {
		patterns[pattern.Name] = pattern
	}
	checked := make(map[[2]string]bool)
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Pattern == "off" || binding.Pattern == "keep" {
				continue
			}
			pair := [2]string{binding.Track, binding.Pattern}
			if checked[pair] {
				continue
			}
			checked[pair] = true
			track, trackOK := tracks[binding.Track]
			pattern, patternOK := patterns[binding.Pattern]
			if !trackOK || !patternOK {
				continue // reference validation reports these errors
			}
			if _, err := CompilePattern(score, pattern, track); err != nil {
				code := "CICADA-PARAM"
				if strings.Contains(err.Error(), "seed") {
					code = "CICADA-SEED"
				} else if strings.Contains(err.Error(), "has no degree") {
					code = "CICADA-SCALE-DEGREE"
				}
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: code, Severity: "error", Message: err.Error(), Position: binding.Position,
				})
			}
		}
	}
	return programs, diagnostics
}
