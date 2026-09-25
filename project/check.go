package project

import (
	"errors"
	"reflect"
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
		if _, err := CompileMixerParams(track); err != nil {
			position := parameterErrorPosition(track, func(single notation.Track) error {
				_, err := CompileMixerParams(single)
				return err
			})
			diagnostics = append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: position})
		}
		if track.Kind == "acid" {
			if _, err := CompileAcidParams(track); err != nil {
				position := parameterErrorPosition(track, func(single notation.Track) error {
					_, err := CompileAcidParams(single)
					return err
				})
				diagnostics = append(diagnostics, notation.Diagnostic{
					Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: position,
				})
			}
		}
		if track.Kind == "drums" {
			if _, err := CompileDrumParams(track); err != nil {
				position := parameterErrorPosition(track, func(single notation.Track) error {
					_, err := CompileDrumParams(single)
					return err
				})
				diagnostics = append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: position})
			}
		}
		program := programs[track.Kind]
		if program == nil {
			continue
		}
		overrides := make(map[string]string, len(track.Params))
		for _, param := range track.Params {
			if param.Name == "level" || param.Name == "pan" || param.Name == "insert" || param.Name == "send_a" || param.Name == "send_b" || param.Name == "send_pre" || param.Name == "bus" {
				continue
			}
			overrides[param.Name] = param.Value
		}
		if _, err := instrument.Lower(program, overrides); err != nil {
			position := parameterErrorPosition(track, func(single notation.Track) error {
				param := single.Params[0]
				if param.Name == "level" || param.Name == "pan" || param.Name == "insert" || param.Name == "send_a" || param.Name == "send_b" || param.Name == "send_pre" || param.Name == "bus" {
					return nil
				}
				_, err := instrument.Lower(program, map[string]string{param.Name: param.Value})
				return err
			})
			diagnostics = append(diagnostics, notation.Diagnostic{
				Code: "CICADA-UNIT", Severity: "error", Message: err.Error(), Position: position,
			})
		}
	}
	patterns := make(map[string]notation.Pattern, len(score.Patterns))
	for _, pattern := range score.Patterns {
		patterns[pattern.Name] = pattern
	}
	checked := make(map[[2]string]bool)
	reached := make(map[string]bool, len(score.Patterns))
	compiledByPattern := make(map[string][]CompiledPattern)
	firstTrack := make(map[string]string)
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Pattern == "off" || binding.Pattern == "keep" || binding.Pattern == "stop" && !scoreHasPattern(score, "stop") {
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
			reached[pattern.Name] = true
			compiled, err := CompilePattern(score, pattern, track)
			if err != nil {
				diagnostics = append(diagnostics, patternCompileDiagnostic(err, binding.Position))
				continue
			}
			if previous, exists := compiledByPattern[pattern.Name]; exists {
				if !reflect.DeepEqual(previous, compiled) {
					diagnostics = append(diagnostics, notation.Diagnostic{
						Code: "CICADA-USE", Severity: "error",
						Message:  "pattern " + pattern.Name + " resolves to different notes on tracks " + firstTrack[pattern.Name] + " and " + track.Name,
						Position: binding.Position,
					})
				}
			} else {
				compiledByPattern[pattern.Name] = compiled
				firstTrack[pattern.Name] = track.Name
			}
		}
	}
	for _, pattern := range score.Patterns {
		if reached[pattern.Name] {
			continue
		}
		if _, err := CompilePattern(score, pattern, representativeTrack(score, pattern)); err != nil {
			diagnostics = append(diagnostics, patternCompileDiagnostic(err, pattern.Position))
		}
	}
	diagnostics = append(diagnostics, checkSourceVoiceBudget(score, tracks)...)
	return programs, diagnostics
}

// The typed project has the same ceiling. Check it here as well so source
// validation can point to the song entry that activates too many voices.
func checkSourceVoiceBudget(score *notation.Score, tracks map[string]notation.Track) []notation.Diagnostic {
	scenes := make(map[string]notation.Scene, len(score.Scenes))
	for _, scene := range score.Scenes {
		scenes[scene.Name] = scene
	}
	kitVoices := make(map[string]int, len(score.Kits))
	for _, kit := range score.Kits {
		kitVoices[kit.Name] = len(kit.Bindings)
	}
	active := make(map[string]string, len(tracks))
	for _, entry := range score.Song {
		scene, ok := scenes[entry.Scene]
		if !ok {
			continue // reference validation reports this error
		}
		for _, binding := range scene.Bindings {
			switch binding.Pattern {
			case "off":
				delete(active, binding.Track)
			case "stop":
				if scoreHasPattern(score, "stop") {
					active[binding.Track] = binding.Pattern
				} else {
					delete(active, binding.Track)
				}
			case "keep":
			default:
				active[binding.Track] = binding.Pattern
			}
		}
		voices := 0
		for trackID := range active {
			kind := tracks[trackID].Kind
			if kind == "drums" {
				voices += len(laneOrder)
			} else if count, ok := kitVoices[kind]; ok {
				voices += count
			} else {
				voices++
			}
		}
		if voices > 32 {
			return []notation.Diagnostic{{
				Code: "CICADA-LIMIT", Severity: "error", Position: entry.Position,
				Message: "scene " + scene.Name + " exceeds 32 simultaneous voices",
			}}
		}
	}
	return nil
}

// Parameter compilers report the first invalid value but do not carry source
// spans. Retry only after a failure to locate the authored value. A failure
// caused by the combination of otherwise valid values stays on the track.
func parameterErrorPosition(track notation.Track, check func(notation.Track) error) notation.Position {
	for _, param := range track.Params {
		single := track
		single.Params = []notation.Param{param}
		if check(single) != nil {
			return param.ValuePosition
		}
	}
	return track.Position
}

func patternCompileDiagnostic(err error, position notation.Position) notation.Diagnostic {
	var stepError *patternCompileError
	if errors.As(err, &stepError) {
		position = stepError.position
	}
	code := "CICADA-PARAM"
	if strings.Contains(err.Error(), "seed") {
		code = "CICADA-SEED"
	} else if strings.Contains(err.Error(), "has no degree") {
		code = "CICADA-SCALE-DEGREE"
	}
	return notation.Diagnostic{Code: code, Severity: "error", Message: err.Error(), Position: position}
}
