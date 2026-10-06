package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type explainLocation struct {
	bar, beat, step int
}

func explainParameter(scorePath, path, location string, output io.Writer) error {
	loc := explainLocation{bar: 1, beat: 1, step: 1}
	if location != "" {
		parsed, err := parseExplainLocation(location)
		if err != nil {
			return err
		}
		loc = parsed
	}
	score, diagnostics, err := project.LoadScore(scorePath, nil)
	if err != nil {
		return err
	}
	if score == nil || hasDiagnosticErrors(diagnostics) {
		return diagnosticError(diagnostics)
	}
	compiled, diagnostics := project.FromScore(score)
	if compiled == nil {
		return diagnosticError(diagnostics)
	}
	lookup := path
	if alias, rest, found := strings.Cut(path, "."); found {
		if namespace, ok := score.LibraryAliases[alias]; ok {
			lookup = namespace + "." + rest
		}
	}
	if origin, ok := score.Origins[lookup]; ok {
		fmt.Fprintf(output, "%s: library %s (source %s:%d:%d)\n", path, origin.Library, filepath.Base(origin.Position.File), origin.Position.Line, origin.Position.Column)
		return nil
	}
	resolved, err := project.ResolveParameterPath(compiled, lookup)
	if err != nil {
		return err
	}
	value, err := project.ParamAddressByName(compiled, lookup)
	if err != nil {
		return err
	}
	active, err := sceneValueAtBar(compiled, lookup, loc.bar)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "%s at bar %d, beat %d, step %d\n", path, loc.bar, loc.beat, loc.step)
	if origin, ok := score.Origins[resolved.Owner]; ok {
		fmt.Fprintf(output, "library: %s\n", origin.Library)
	}
	for _, track := range score.Tracks {
		if track.Name == resolved.Owner {
			if origin, ok := score.Origins[track.Kind]; ok {
				fmt.Fprintf(output, "instrument: %s (library %s)\n", track.Kind, origin.Library)
			}
		}
	}
	fmt.Fprintf(output, "registry default: %s\n", explainDefault(resolved.Descriptor))
	if sourceHasBlockSetting(score, resolved) {
		fmt.Fprintf(output, "%s block (%s): %s\n", resolved.OwnerKind, resolved.Owner, explainValue(value.Value, resolved.Descriptor))
	}
	computedText := explainValue(value.Value, resolved.Descriptor)
	if active.set {
		computedText = explainProjectValue(active.value, resolved.Descriptor)
		fmt.Fprintf(output, "scene %s (entered bar %d): %s\n", active.scene, active.bar, explainProjectValue(active.value, resolved.Descriptor))
	}
	fmt.Fprintf(output, "computed: %s\n", computedText)
	return nil
}

func parseExplainLocation(value string) (explainLocation, error) {
	if !strings.HasPrefix(value, "@") {
		return explainLocation{}, fmt.Errorf("location must start with @bar[.beat[.step]]")
	}
	parts := strings.Split(strings.TrimPrefix(value, "@"), ".")
	if len(parts) < 1 || len(parts) > 3 {
		return explainLocation{}, fmt.Errorf("location must be @bar[.beat[.step]]")
	}
	values := []int{1, 1, 1}
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 1 {
			return explainLocation{}, fmt.Errorf("location bars, beats, and steps start at 1")
		}
		values[i] = number
	}
	return explainLocation{bar: values[0], beat: values[1], step: values[2]}, nil
}

type sceneExplainValue struct {
	set   bool
	value project.SceneValue
	scene string
	bar   int
}

func sceneValueAtBar(p *project.Project, path string, targetBar int) (sceneExplainValue, error) {
	bar := 1
	var active sceneExplainValue
	foundTarget := false
	for _, entry := range p.Song {
		var scene *project.Scene
		for i := range p.Scenes {
			if p.Scenes[i].ID == entry.Scene {
				scene = &p.Scenes[i]
				break
			}
		}
		if scene == nil {
			return sceneExplainValue{}, fmt.Errorf("song refers to unknown scene %q", entry.Scene)
		}
		if targetBar >= bar && targetBar < bar+int(entry.Bars) {
			foundTarget = true
		}
		for _, setting := range scene.Settings {
			if setting.Path == path {
				active = sceneExplainValue{set: true, value: setting.Value, scene: scene.ID, bar: bar}
			}
		}
		if foundTarget {
			break
		}
		bar += int(entry.Bars)
	}
	if !foundTarget {
		return sceneExplainValue{}, fmt.Errorf("bar %d is outside the song (which has %d bars)", targetBar, bar-1)
	}
	return active, nil
}

func sourceHasBlockSetting(score *notation.Score, resolved project.ResolvedParam) bool {
	switch resolved.OwnerKind {
	case "track":
		for _, track := range score.Tracks {
			if track.Name != resolved.Owner {
				continue
			}
			for _, setting := range track.Params {
				if setting.Name == resolved.Descriptor.Source {
					return true
				}
			}
		}
	case "effect":
		for _, effect := range score.Effects {
			if effect.Name != resolved.Owner {
				continue
			}
			for _, setting := range effect.Params {
				if setting.Name == resolved.Descriptor.Source {
					return true
				}
			}
		}
	}
	return false
}

func explainDefault(descriptor paramdefs.Descriptor) string {
	if descriptor.Curve == "enum" && int(descriptor.Default) >= 0 && int(descriptor.Default) < len(descriptor.Values) {
		return descriptor.Values[int(descriptor.Default)]
	}
	if descriptor.Curve == "toggle" {
		if descriptor.Default != 0 {
			return "on"
		}
		return "off"
	}
	return explainValue(descriptor.Default, descriptor)
}

func explainProjectValue(value project.SceneValue, descriptor paramdefs.Descriptor) string {
	if value.Unit == "enum" {
		return value.Text
	}
	if value.Number == nil {
		return "off"
	}
	return explainValue(*value.Number, descriptor)
}

func explainValue(value any, descriptor paramdefs.Descriptor) string {
	if value == nil {
		return "off"
	}
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case string:
		return typed
	default:
		return fmt.Sprint(value)
	}
	formatted := strconv.FormatFloat(number, 'f', -1, 64)
	unit := descriptor.Unit
	if unit == "unit" || unit == "ratio" || unit == "semitone" {
		unit = ""
	}
	if unit == "" {
		return formatted
	}
	return formatted + unit
}

func diagnosticError(diagnostics []notation.Diagnostic) error {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return &project.SourceError{Diagnostic: diagnostic}
		}
	}
	return fmt.Errorf("score could not be parsed")
}
