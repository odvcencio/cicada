package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

// Grid and played phrases share the same route into the song. Recording a
// performance must not remove arrangement or variation controls from its editor.
func (s *studioApp) patternManagement(ctx *server.Context, v workspace, csrf string, pattern project.Pattern) gosx.Node {
	q := ctx.Request.URL.Query()
	returnTo := ctx.Request.URL.RequestURI()
	form := func(name string, children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/" + name, CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form"}, append([]gosx.Node{hidden("source_revision", v.SourceRevision)}, children...)...)
	}
	state := action.States(ctx.Request)["pattern"]
	failed := !state.OK() && state.Value("pattern") == pattern.ID && (state.Value("action") == "duplicate" || state.Value("action") == "bind")
	value := func(operation, name, fallback string) string {
		if failed && state.Value("action") == operation {
			return state.Value(name)
		}
		return fallback
	}
	variation := form("pattern", hidden("action", "duplicate"), hidden("pattern", pattern.ID), field("New pattern name", textInput("newName", value("duplicate", "newName", nextPatternName(v.Project, pattern.ID)))), submit("", "", "Create variation"))
	var tracks []string
	preferred := q.Get("track")
	for _, track := range v.Project.Tracks {
		if project.PatternTrackCompatible(v.Project, pattern, track) {
			tracks = append(tracks, track.ID)
		}
	}
	if len(tracks) > 0 && !slices.Contains(tracks, preferred) {
		preferred = tracks[0]
	}
	var destination gosx.Node
	if len(tracks) == 0 {
		destination = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Add a compatible instrument or drum track to use this pattern in the song."))
	} else if v.Project.Arrange != nil {
		var end int64
		var uses []gosx.Node
		for _, place := range v.Project.Arrange.Placements {
			end = max(end, place.AtTick+place.LengthTicks)
			if place.Content == pattern.ID {
				uses = append(uses, gosx.El("li", gosx.El("a", gosx.Attrs(gosx.Attr("href", placementURL(place.ID))), gosx.Text(place.ID+" · "+place.Track+" · "+placementPosition(place.AtTick)))))
			}
		}
		end = (end + seq.TicksPerStep - 1) / seq.TicksPerStep * seq.TicksPerStep
		length := pattern.DurationTicks()
		values := map[string]string{"newName": patternPlacementName(v.Project, pattern.ID), "track": preferred, "at": placementLiteral(end, true), "length": strconv.FormatInt(length, 10) + "ticks"}
		placeState := action.States(ctx.Request)["arrange"]
		placeFailed := !placeState.OK() && placeState.Value("action") == "append" && placeState.Value("content") == pattern.ID
		if placeFailed {
			for key := range values {
				values[key] = placeState.Value(key)
			}
			failed = true
		}
		destination = gosx.Fragment(
			form("arrange", hidden("action", "append"), hidden("content", pattern.ID), field("Placement name", textInput("newName", values["newName"])), field("Track", selectInput("track", values["track"], tracks)), field("Start", textInput("at", values["at"])), field("Length", textInput("length", values["length"])), submit("", "", "Place in song")),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Start uses @bar.beat.step or exact ticks. Length starts at one complete pattern loop. Each placement restarts the phrase; later pattern edits affect every use. Undo removes this placement.")),
			gosx.El("ul", gosx.Fragment(uses...)))
	} else if len(v.Project.Scenes) > 0 {
		var scenes []string
		for _, scene := range v.Project.Scenes {
			scenes = append(scenes, scene.ID)
		}
		destination = form("pattern", hidden("action", "bind"), hidden("pattern", pattern.ID), field("Scene", selectInput("scene", value("bind", "scene", q.Get("scene")), scenes)), field("Track", selectInput("track", value("bind", "track", preferred), tracks)), submit("binding", "play", "Assign to scene"), submit("binding", "off", "Stop in this scene"), submit("binding", "keep", "Carry previous pattern"), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Assign replaces this track’s pattern wherever the scene is used. Scene choices carry forward until changed or stopped. Stop releases this track; Carry keeps its earlier selection. Undo restores the previous assignment.")))
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management"), gosx.Attr("data-pattern-management", pattern.ID), gosx.Attr("data-gosx-key", "pattern-management-"+pattern.ID), gosx.Attr("open", failed)), gosx.El("summary", gosx.Text("Use this pattern")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), variation, destination))
}

func patternPlacementName(p *project.Project, pattern string) string {
	base := strings.ReplaceAll(pattern, ".", "-")
	if len(base) > 48 {
		base = base[:48]
	}
	base += "-place"
	used := map[string]bool{}
	for _, place := range p.Arrange.Placements {
		used[place.ID] = true
	}
	for _, marker := range p.Arrange.Markers {
		used[marker.ID] = true
	}
	name := base
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}
