package project

import (
	"path"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

// EditPlan builds the pure edit.Plan view of a compiled project. Slices keep
// source order, so plan indices equal engine indices. files supplies the authored
// project sources, including templates omitted by compilation. When omitted,
// source supplies the authored names in the current buffer.
func EditPlan(p *Project, source []byte, files ...notation.SourceFile) *edit.Plan {
	plan := &edit.Plan{Revision: edit.Revision(source), Names: map[string]bool{}}
	if p == nil {
		return plan
	}
	if len(files) == 0 {
		files = []notation.SourceFile{{Source: source}}
	}
	for _, file := range files {
		prefix := ""
		if file.Library != "" {
			prefix = strings.ReplaceAll(file.Library, "/", ".") + "."
		}
		for name := range notation.DeclarationNames([]notation.SourceFile{file}) {
			plan.Names[prefix+strings.Join(strings.Fields(name), "")] = true
		}
		for _, imp := range notation.ReadImports(file) {
			plan.Names[prefix+path.Base(imp.Path)] = true
		}
	}
	plan.Edition = p.Edition
	plan.Title = p.Title
	plan.TempoMilli = p.TempoMilli
	plan.KeyRoot = p.Key.Root
	plan.Scale = p.Key.Scale
	for _, track := range p.Tracks {
		plan.Tracks = append(plan.Tracks, edit.Track{ID: track.ID, Kind: track.Kind, Polyphony: GraphPolyphony(p, track.Kind), Slots: track.Slots})
		plan.Names[track.ID] = true
	}
	for _, pattern := range p.Patterns {
		out := edit.Pattern{ID: pattern.ID, Kind: pattern.Kind, Steps: pattern.Steps, StepTicks: pattern.StepTicks, Transpose: pattern.Transpose, Data: editSteps(pattern.Data)}
		if pattern.Lanes != nil {
			out.Lanes = make(map[string][]*edit.Step, len(pattern.Lanes))
			for lane, steps := range pattern.Lanes {
				out.Lanes[lane] = editSteps(steps)
			}
		}
		for _, expression := range pattern.Expression {
			out.Expression = append(out.Expression, edit.NoteExpression{PitchCents: expression.PitchCents, Pressure: expression.Pressure, Timbre: expression.Timbre, VibratoDepthCents: expression.VibratoDepthCents})
		}
		plan.Patterns = append(plan.Patterns, out)
		plan.Names[pattern.ID] = true
	}
	for _, scene := range p.Scenes {
		out := edit.Scene{ID: scene.ID, Bindings: scene.Bindings}
		for _, setting := range scene.Settings {
			out.Settings = append(out.Settings, edit.Setting{Path: setting.Path, Unit: setting.Value.Unit, Number: setting.Value.Number, Text: setting.Value.Text})
		}
		plan.Scenes = append(plan.Scenes, out)
		plan.Names[scene.ID] = true
	}
	for _, entry := range p.Song {
		plan.Song = append(plan.Song, edit.SongEntry{Scene: entry.Scene, Bars: int(entry.Bars)})
	}
	for _, placement := range arrangementPlacements(p) {
		plan.Placements = append(plan.Placements, edit.Placement{ID: placement.ID, Track: placement.Track, Content: placement.Content, AtTick: placement.AtTick, LengthTicks: placement.LengthTicks})
		plan.Names[placement.ID] = true
	}
	if p.Arrange != nil {
		for _, marker := range p.Arrange.Markers {
			plan.Markers = append(plan.Markers, edit.Marker{ID: marker.ID, AtTick: marker.AtTick})
			plan.Names[marker.ID] = true
		}
	}
	for _, clip := range p.Clips {
		plan.Clips = append(plan.Clips, edit.Clip{ID: clip.Name, Asset: clip.Asset, StartFrame: clip.StartFrame, EndFrame: clip.EndFrame})
		plan.Names[clip.Name] = true
	}
	for _, sampler := range p.Samplers {
		plan.Names[sampler.Name] = true
	}
	for _, instrument := range p.Instruments {
		plan.Names[instrument.ID] = true
	}
	for _, kit := range p.Kits {
		plan.Names[kit.ID] = true
	}
	for _, effect := range p.Effects {
		plan.Names[effect.ID] = true
	}
	for _, bus := range p.Buses {
		plan.Names[bus.ID] = true
	}
	for _, asset := range p.Assets {
		plan.Names[asset.Name] = true
	}
	for _, export := range p.Exports {
		plan.Names[export.ID] = true
	}
	values := make(map[string]float64)
	for _, address := range ParamAddresses(p) {
		if value, ok := address.Value.(float64); ok {
			values[address.Address] = value
		}
	}
	plan.ResolveParam = func(path string) (edit.Param, error) {
		resolved, err := ResolveParameterPath(p, path)
		if err != nil {
			return edit.Param{}, err
		}
		param := edit.Param{Path: path, Descriptor: resolved.Descriptor}
		if value, ok := values[path]; ok {
			param.Value = &value
		}
		return param, nil
	}
	return plan
}

func editSteps(steps []*Step) []*edit.Step {
	if steps == nil {
		return nil
	}
	out := make([]*edit.Step, len(steps))
	for i, step := range steps {
		if step == nil {
			continue
		}
		out[i] = &edit.Step{Note: step.Note, Accent: step.Accent, Slide: step.Slide, Tie: step.Tie, Ratchet: step.Ratchet, Probability: step.Probability, Velocity: step.Velocity, Notes: append([]int(nil), step.Notes...)}
	}
	return out
}
