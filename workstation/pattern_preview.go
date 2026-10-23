package main

import (
	"encoding/json"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) patternPreview(ctx *server.Context, v workspace, csrf string, pattern project.Pattern) gosx.Node {
	var tracks []project.Track
	preferred := ctx.Request.URL.Query().Get("track")
	for _, track := range v.Project.Tracks {
		if !project.PatternTrackCompatible(v.Project, pattern, track) {
			continue
		}
		tracks = append(tracks, track)
		if preferred == "" {
			for _, slot := range track.Slots {
				if slot != nil && *slot == pattern.ID {
					preferred = track.ID
					break
				}
			}
		}
	}
	if len(tracks) == 0 {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Add a compatible instrument or drum track to hear this part."))
	}
	validPreferred := false
	for _, track := range tracks {
		validPreferred = validPreferred || track.ID == preferred
	}
	if !validPreferred {
		preferred = tracks[0].ID
	}
	var choices []gosx.Node
	for _, track := range tracks {
		choices = append(choices, gosx.El("option", gosx.Attrs(gosx.Attr("value", track.ID), gosx.Attr("selected", track.ID == preferred)), gosx.Text(track.ID+" · "+track.Kind)))
	}
	props, _ := json.Marshal(map[string]string{"csrf": csrf, "pattern": pattern.ID, "revision": v.Revision})
	return meterEngine(ctx, engine.Config{Name: "CicadaPatternPreview", Kind: engine.KindSurface, MountID: "cicada-pattern-preview", Runtime: engine.RuntimeGoWASM, Props: props, Capabilities: []engine.Capability{engine.CapFetch, engine.CapKeyboard}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}},
		gosx.Fragment(
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")),
				field("Hear with", gosx.El("select", gosx.Attrs(gosx.Attr("data-pattern-preview-track", ""), gosx.Attr("aria-label", "Hear pattern with")), gosx.Fragment(choices...))),
				gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-sound-preview", pattern.ID), gosx.Attr("data-sound-name", pattern.ID), gosx.Attr("aria-label", "Loop "+pattern.ID), gosx.Attr("aria-pressed", "false"), gosx.BoolAttr("disabled")), gosx.Text("Loop part"))),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.Attr("data-sound-status", ""), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite")), gosx.Text("Preparing part preview…"))))
}
