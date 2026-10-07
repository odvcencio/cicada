package main

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) instruments(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score to open instruments"))
	}
	p := v.Project
	var library []gosx.Node
	for _, patch := range instrument.Patches() {
		instrumentName := availableInstrumentName(p, patch.ID)
		trackName := availableInstrumentTrack(p, patch.ID)
		voices := "8-note polyphony"
		if patch.Mode == "mono" {
			voices = "Monophonic"
		}
		form := s.form(v, csrf, "instruments", "instrument", hidden("action", "add-preset"), hidden("pattern", patch.ID), field("Instrument name", textInput("newName", instrumentName)), field("New track name", textInput("track", trackName)), submit("", "", "Add instrument and track"))
		library = append(library, gosx.El("details", gosx.Attrs(gosx.Attr("class", "arrangement-block"), gosx.Attr("data-instrument-patch", patch.ID)), gosx.El("summary", gosx.El("strong", gosx.Text(patch.Name)), gosx.Text(" · "+voices)), gosx.El("p", gosx.Text(patch.Description)), form))
	}
	var tracks []gosx.Node
	for _, track := range p.Tracks {
		var definition *project.Instrument
		for i := range p.Instruments {
			if p.Instruments[i].ID == track.Kind {
				definition = &p.Instruments[i]
				break
			}
		}
		if definition == nil {
			for _, sampler := range p.Samplers {
				if sampler.Name == track.Kind {
					label := "Sampled"
					if strings.HasSuffix(sampler.Name, "_model") {
						label = "Model approximation"
					}
					tracks = append(tracks, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip"), gosx.Attr("data-instrument-track", track.ID)), gosx.El("h3", gosx.Text(track.ID)), gosx.El("p", gosx.Text(label+" · "+sampler.Name))))
				}
			}
			continue
		}
		mode := "Monophonic"
		if definition.Mode == "poly" {
			mode = "8-note polyphony"
		}
		var parameters []gosx.Node
		for _, param := range definition.Params {
			value := strconv.FormatFloat(param.Default, 'f', -1, 64)
			defaultText := value + instrumentUnit(param.Unit)
			label := param.ID + " · default"
			if override, ok := track.Params[param.ID]; ok {
				label = param.ID + " · track override"
				if override.Number != nil {
					value = strconv.FormatFloat(*override.Number, 'f', -1, 64)
				} else {
					value = override.Text
				}
			}
			if unit := instrumentUnit(param.Unit); unit != "" {
				label += " (" + unit + ")"
			}
			input := gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("step", "any"), gosx.Attr("name", "value"), gosx.Attr("value", value)))
			reset := gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", "reset-parameter"), gosx.BoolAttr("formnovalidate")), gosx.Text("Use default"))
			form := s.form(v, csrf, "instruments", "instrument", hidden("track", track.ID), hidden("newName", param.ID), field(label, input), submit("action", "set-parameter", "Apply"), reset)
			parameters = append(parameters, gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-control"), gosx.Attr("data-gosx-key", "parameter-"+param.ID)), form, gosx.El("small", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Default: "+defaultText))))
		}
		tracks = append(tracks, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip"), gosx.Attr("data-instrument-track", track.ID), gosx.Attr("data-gosx-key", "instrument-"+track.ID)), gosx.El("h3", gosx.Text(track.ID)), gosx.El("p", gosx.Text(definition.ID+" · "+mode)), gosx.Fragment(parameters...)))
	}
	if len(tracks) == 0 {
		tracks = append(tracks, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Add a patch to create your first programmable instrument track.")))
	}
	return gosx.Fragment(s.recorded(ctx, v, csrf), s.samplePacks(ctx, v, csrf), ui.Panel(ui.PanelProps{ID: "instrument-library", Title: "Instrument library", Description: "Add a voiced starting point, then make it yours. Each patch is saved as an editable instrument graph in your score."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrangement-blocks")), gosx.Fragment(library...)), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Choose a notes pattern in Patterns and place it on the new track. Undo removes the instrument and track together."))), ui.Panel(ui.PanelProps{ID: "project-instruments", Title: "Your instruments", Description: "Shape each track with the declared controls. Use default removes its override; Undo restores any setting."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "strips")), gosx.Fragment(tracks...)), gosx.El("p", gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=code")), gosx.Text("Edit the shared instrument graph in Score")))))
}

func instrumentUnit(unit string) string {
	switch strings.ToLower(unit) {
	case "hz":
		return "Hz"
	case "ms":
		return "ms"
	case "db":
		return "dB"
	default:
		return ""
	}
}

func availableInstrumentName(p *project.Project, base string) string {
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		used := false
		for _, value := range p.Instruments {
			used = used || value.ID == name
		}
		for _, value := range p.Samplers {
			used = used || value.Name == name
		}
		for _, value := range p.Kits {
			used = used || value.ID == name
		}
		if !used {
			return name
		}
	}
}

func availableInstrumentTrack(p *project.Project, base string) string {
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		used := false
		for _, value := range p.Tracks {
			used = used || value.ID == name
		}
		if !used {
			return name
		}
	}
}
