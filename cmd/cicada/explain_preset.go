package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func explainPresetLayers(score *notation.Score, owner, name string, output io.Writer) {
	for _, track := range score.Tracks {
		if track.Name != owner {
			continue
		}
		p, ok := notation.FindPreset(score, track.Kind)
		if !ok {
			return
		}
		name = presetSourceParamName(score, owner, name)
		for _, inst := range score.Instruments {
			if inst.Name == p.Target {
				for _, q := range inst.Params {
					if q.Name == name {
						fmt.Fprintf(output, "instrument default (%s): %s\n", inst.Name, q.Default)
					}
				}
			}
		}
		for _, q := range p.Params {
			if q.Name == name {
				fmt.Fprintf(output, "preset (%s): %s\n", p.Name, q.Value)
			}
		}
	}
	for _, effect := range score.Effects {
		if effect.Name != owner {
			continue
		}
		p, ok := notation.FindPreset(score, effect.Kind)
		if !ok {
			return
		}
		for _, target := range score.Effects {
			if target.Name == p.Target {
				for _, q := range target.Params {
					if q.Name == name {
						fmt.Fprintf(output, "instrument default (%s): %s\n", target.Name, q.Value)
					}
				}
			}
		}
		for _, q := range p.Params {
			if q.Name == name {
				fmt.Fprintf(output, "preset (%s): %s\n", p.Name, q.Value)
			}
		}
	}
}

func explainBlockValue(score *notation.Score, resolved project.ResolvedParam, fallback any) string {
	var params []notation.Param
	for _, t := range score.Tracks {
		if t.Name == resolved.Owner {
			params = t.Params
		}
	}
	for _, e := range score.Effects {
		if e.Name == resolved.Owner {
			params = e.Params
		}
	}
	for _, p := range params {
		if p.Name == presetSourceParamName(score, resolved.Owner, resolved.Descriptor.Source) {
			return p.Value
		}
	}
	return explainValue(fallback, resolved.Descriptor)
}

// presetSourceParamName maps lowered drum names back to the lane preset body.
func presetSourceParamName(score *notation.Score, owner, name string) string {
	for _, track := range score.Tracks {
		if track.Name != owner {
			continue
		}
		if p, ok := notation.FindPreset(score, track.Kind); ok && strings.HasPrefix(p.Target, "builtin.") {
			return strings.TrimPrefix(name, strings.TrimPrefix(p.Target, "builtin.")+"_")
		}
	}
	return name
}

// Host-only parameters have no live kernel slot, but keep the same explain
// layers as built-in registry parameters.
func explainAuthoredParameter(score *notation.Score, compiled *project.Project, path string, loc explainLocation, output io.Writer) (bool, error) {
	owner, name, ok := strings.Cut(path, ".")
	if !ok {
		return false, nil
	}
	for _, track := range score.Tracks {
		if track.Name != owner {
			continue
		}
		target := track.Kind
		preset, hasPreset := notation.FindPreset(score, target)
		if hasPreset {
			target = preset.Target
		}
		value, registry, found := "", "none (authored parameter)", false
		for _, inst := range score.Instruments {
			if inst.Name != target {
				continue
			}
			for _, param := range inst.Params {
				if param.Name == name {
					value, found = param.Default, true
				}
			}
			if !found && name == "octave" {
				value, registry, found = strconv.Itoa(inst.Octave), "3", true
			}
		}
		if target == "acid" && name == "octave" {
			value, registry, found = "3", "3", true
		}
		for _, sampler := range score.Samplers {
			if sampler.Name != target || name != "root" && name != "mode" && name != "voices" {
				continue
			}
			for _, param := range sampler.Params {
				if param.Name == name {
					value, registry, found = param.Value, "none (sampler setting)", true
				}
			}
		}
		if !found {
			continue
		}
		if _, err := sceneValueAtBar(compiled, path, loc.bar); err != nil {
			return true, err
		}
		fmt.Fprintf(output, "%s at bar %d, beat %d, step %d\n", path, loc.bar, loc.beat, loc.step)
		fmt.Fprintf(output, "registry default: %s\n", registry)
		if target != "acid" {
			fmt.Fprintf(output, "instrument default (%s): %s\n", target, value)
		}
		for _, p := range preset.Params {
			if p.Name == name {
				value = p.Value
				fmt.Fprintf(output, "preset (%s): %s\n", preset.Name, value)
			}
		}
		for _, p := range track.Params {
			if p.Name == name {
				value = p.Value
				fmt.Fprintf(output, "track block (%s): %s\n", owner, value)
			}
		}
		fmt.Fprintf(output, "computed: %s\n", value)
		return true, nil
	}
	return false, nil
}
