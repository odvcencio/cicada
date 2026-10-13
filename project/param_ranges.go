package project

import (
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// checkParamRanges compares every voice, drum, effect, master-chain and track-EQ
// setting in a score with the host parameter table (package paramdefs: the
// registry plus HostOnly). It runs before the kernel compilers, reports every
// out-of-range setting in the score, and names the parameter, the value as
// written and the allowed range. The kernel Validate functions stay as the
// backstop; a test keeps the table and the kernel in agreement.
//
// The returned set holds the value position of each reported setting, so the
// compilers that follow can skip those settings and not repeat the error in
// less specific words.
func checkParamRanges(score *notation.Score) ([]notation.Diagnostic, map[notation.Position]bool) {
	var diagnostics []notation.Diagnostic
	flagged := map[notation.Position]bool{}
	report := func(param notation.Param, descriptor paramdefs.Descriptor, label string, keyboard bool) {
		if strings.HasPrefix(param.Value, "\"") {
			return
		}
		check := paramdefs.RangeMessage
		if keyboard {
			check = paramdefs.RangeMessageKeyboard
		}
		message := check(descriptor, label+" "+param.Name, param.Value)
		if message == "" {
			return
		}
		position := param.ValuePosition
		if position.Line == 0 {
			position = param.Position
		}
		flagged[param.ValuePosition] = true
		diagnostics = append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: message, Position: position})
	}
	customKits := make(map[string]bool, len(score.Kits))
	for _, kit := range score.Kits {
		customKits[kit.Name] = true
	}
	for _, track := range score.Tracks {
		if customKits[track.Kind] {
			continue // lane bindings give these tracks their own parameter names
		}
		voice := notation.ParameterVoiceKind(score, track.Kind)
		if voice == "instrument" {
			continue // authored parameters declare no bounds
		}
		// A modeled keyboard track is compiled by KeysSpecFromValues for every
		// setting it accepts, the ones it inherits from the piano included.
		keyboard := false
		switch voice {
		case "ep", "clav", "organ", "fm", "analog", "strings":
			keyboard = true
		}
		for _, param := range track.Params {
			if param.Name == "octave" {
				continue // checked with the instrument octave rules
			}
			if descriptor, ok := paramdefs.TrackParam(voice, param.Name); ok {
				report(param, descriptor, track.Kind, keyboard)
			}
		}
	}
	master := map[string]bool{}
	for _, param := range score.Master {
		if param.Name == "insert" {
			for _, name := range strings.Fields(param.Value) {
				master[name] = true
			}
		}
	}
	for _, effect := range score.Effects {
		kind := effect.Kind
		if kind == "" {
			kind = effect.Name
		}
		for _, param := range effect.Params {
			if param.Name == "sidechain" {
				continue // names a bus, which a score can declare
			}
			if descriptor, ok := paramdefs.DeviceParam(kind, param.Name, master[effect.Name]); ok {
				report(param, descriptor, kind, false)
			}
		}
	}
	return diagnostics, flagged
}

// withoutFlagged returns the track without the settings that the range check
// already reported.
func withoutFlagged(track notation.Track, flagged map[notation.Position]bool) notation.Track {
	if len(flagged) == 0 {
		return track
	}
	kept := make([]notation.Param, 0, len(track.Params))
	for _, param := range track.Params {
		if !flagged[param.ValuePosition] {
			kept = append(kept, param)
		}
	}
	track.Params = kept
	return track
}
