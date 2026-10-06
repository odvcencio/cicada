package notation

import (
	"math"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
)

// validateSceneSettings resolves the edition-1 path namespace and rejects any
// target that cannot be previewed safely by the live kernel.
func validateSceneSettings(score *Score, add func(string, string, string, Position)) {
	for _, scene := range score.Scenes {
		seen := make(map[string]bool, len(scene.Settings))
		for _, setting := range scene.Settings {
			if seen[setting.Path] {
				add("CICADA-DUPLICATE", "scene sets path more than once: "+setting.Path, "error", setting.Position)
			}
			seen[setting.Path] = true
			masterControl := false
			for _, param := range score.Master {
				if param.Name == "insert" {
					for _, name := range strings.Fields(param.Value) {
						masterControl = masterControl || name != "->" && strings.HasPrefix(setting.Path, name+".")
					}
				}
			}
			if masterControl {
				add("CICADA-UNSUPPORTED", "master effect controls are fixed until the score is recompiled", "error", setting.Position)
				continue
			}
			descriptor, _, _, code, message := resolveNotationPath(score, setting.Path)
			if code != "" {
				add(code, message, "error", setting.Position)
				continue
			}
			if !descriptor.Live {
				add("CICADA-UNSUPPORTED", "scene setting "+setting.Path+" is not live", "error", setting.Position)
				continue
			}
			if err := validateSceneValue(descriptor, setting.Value); err != nil {
				add("CICADA-UNIT", "invalid value for "+setting.Path+": "+err.Error(), "error", setting.ValuePosition)
			}
		}
	}
}

func resolveNotationPath(score *Score, path string) (paramdefs.Descriptor, string, string, string, string) {
	parts := strings.Split(path, ".")
	if len(parts) == 1 {
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope == "global" && descriptor.Path == path {
				return descriptor, "global", path, "", ""
			}
		}
		return paramdefs.Descriptor{}, "", "", "CICADA-REFERENCE", "unknown parameter path owner or setting " + path
	}
	owner := parts[0]
	for _, effect := range score.Effects {
		if strings.HasPrefix(path, effect.Name+".") && len(effect.Name) > len(owner) {
			owner = effect.Name
		}
	}
	parts = append([]string{owner}, strings.Split(strings.TrimPrefix(path, owner+"."), ".")...)
	track, trackOK := Track{}, false
	for _, candidate := range score.Tracks {
		if candidate.Name == owner {
			track, trackOK = candidate, true
			break
		}
	}
	effectOK := false
	effectKind := ""
	for _, effect := range score.Effects {
		if effect.Name == owner {
			effectOK = true
			effectKind = effect.Kind
			break
		}
	}
	busOK := owner == "music" || owner == "sfx"
	for _, bus := range score.Buses {
		busOK = busOK || bus.Name == owner
	}
	if owner == "master" && !score.HasMaster {
		return paramdefs.Descriptor{}, "master", owner, "CICADA-UNSUPPORTED", "scene setting " + path + " requires a master block"
	}
	if trackOK && effectOK {
		return paramdefs.Descriptor{}, "", owner, "CICADA-REFERENCE", "ambiguous path owner " + owner + "; hint: rename the track or effect so this path resolves to one owner"
	}
	if trackOK {
		setting := strings.Join(parts[1:], ".")
		kind := notationParameterVoiceKind(score, track.Kind)
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope != "track" || descriptor.Path != setting || !contains(descriptor.Voices, kind) {
				continue
			}
			return descriptor, "track", owner, "", ""
		}
		return paramdefs.Descriptor{}, "track", owner, "CICADA-PARAM", "unknown setting in parameter path " + path
	}
	if effectOK {
		setting := strings.Join(parts[1:], ".")
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope == "global" && descriptor.Path == effectKind+"."+setting {
				return descriptor, "effect", owner, "", ""
			}
		}
		return paramdefs.Descriptor{}, "effect", owner, "CICADA-PARAM", "unknown setting in parameter path " + path
	}
	if owner == "master" || busOK {
		scope := "bus"
		if owner == "master" {
			scope = "master"
		}
		setting := strings.Join(parts[1:], ".")
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope == scope && descriptor.Path == setting {
				return descriptor, scope, owner, "", ""
			}
		}
		return paramdefs.Descriptor{}, scope, owner, "CICADA-PARAM", "unknown setting in parameter path " + path
	}
	return paramdefs.Descriptor{}, "", owner, "CICADA-REFERENCE", "unknown parameter path owner " + owner
}

func notationParameterVoiceKind(score *Score, kind string) string {
	switch kind {
	case "acid":
		return "acid"
	case "drums":
		return "drums"
	}
	for _, kit := range score.Kits {
		if kit.Name == kind {
			return "drums"
		}
	}
	if notationModeledPiano(score, kind) {
		return "piano"
	}
	return "instrument"
}

func notationModeledPiano(score *Score, kind string) bool {
	if kind != "piano" || scoreHasSampler(score, kind) {
		return false
	}
	for _, instrument := range score.Instruments {
		if instrument.Name == kind {
			return false
		}
	}
	for _, kit := range score.Kits {
		if kit.Name == kind {
			return false
		}
	}
	return true
}

func validateSceneValue(descriptor paramdefs.Descriptor, source string) error {
	if source == "off" && descriptor.Off {
		return nil
	}
	if descriptor.Curve == "toggle" {
		if source == "on" || source == "off" || source == "true" || source == "false" {
			return nil
		}
	}
	for _, candidate := range descriptor.Values {
		if source == candidate {
			return nil
		}
	}
	value, unit, err := notationBaseValue(source)
	if err != nil {
		return err
	}
	want := strings.ToLower(descriptor.Unit)
	if want == "ratio" || want == "" || want == "semitone" {
		want = "unit"
	}
	if unit != want {
		return strconv.ErrSyntax
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < descriptor.Min || value > descriptor.Max {
		return strconv.ErrRange
	}
	if descriptor.Curve == "toggle" && value != 0 && value != 1 {
		return strconv.ErrRange
	}
	return nil
}

func notationBaseValue(source string) (float64, string, error) {
	unit, scale := "unit", 1.0
	for _, suffix := range []struct {
		text, unit string
		scale      float64
	}{{"khz", "hz", 1000}, {"hz", "hz", 1}, {"ms", "ms", 1}, {"db", "db", 1}, {"s", "ms", 1000}, {"%", "unit", .01}} {
		if strings.HasSuffix(strings.ToLower(source), suffix.text) {
			unit, scale = suffix.unit, suffix.scale
			source = source[:len(source)-len(suffix.text)]
			break
		}
	}
	value, err := strconv.ParseFloat(source, 64)
	if err != nil {
		return 0, "", err
	}
	return value * scale, unit, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
