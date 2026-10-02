package main

import (
	"fmt"
	"io"
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
		if p.Name == resolved.Descriptor.Source {
			return p.Value
		}
	}
	return explainValue(fallback, resolved.Descriptor)
}

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
		for _, inst := range score.Instruments {
			if inst.Name != target {
				continue
			}
			for _, param := range inst.Params {
				if param.Name != name {
					continue
				}
				if _, err := sceneValueAtBar(compiled, path, loc.bar); err != nil {
					return true, err
				}
				fmt.Fprintf(output, "%s at bar %d, beat %d, step %d\n", path, loc.bar, loc.beat, loc.step)
				fmt.Fprintln(output, "registry default: none (authored parameter)")
				fmt.Fprintf(output, "instrument default (%s): %s\n", target, param.Default)
				current := param.Default
				for _, p := range preset.Params {
					if p.Name == name {
						current = p.Value
						fmt.Fprintf(output, "preset (%s): %s\n", preset.Name, current)
					}
				}
				for _, p := range track.Params {
					if p.Name == name {
						current = p.Value
						fmt.Fprintf(output, "track block (%s): %s\n", owner, current)
					}
				}
				fmt.Fprintf(output, "computed: %s\n", current)
				return true, nil
			}
		}
	}
	return false, nil
}
