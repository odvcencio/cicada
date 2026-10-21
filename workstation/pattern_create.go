package main

import (
	"strconv"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func patternKindForTrack(p *project.Project, track project.Track) string {
	switch track.Kind {
	case "audio":
		return ""
	case "acid", "drums":
		return track.Kind
	}
	for _, kit := range p.Kits {
		if kit.ID == track.Kind {
			return "drums"
		}
	}
	return "notes"
}

func availablePatternName(p *project.Project, track string) string {
	base := strings.ReplaceAll(track, ".", "-") + "-pattern"
	if track == "" {
		base = "new-pattern"
	}
	if len(base) > 58 {
		base = base[:58]
	}
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		used := false
		for _, pattern := range p.Patterns {
			used = used || pattern.ID == name
		}
		for _, clip := range p.Clips {
			used = used || clip.Name == name
		}
		if !used {
			return name
		}
	}
}

// The same native/enhanced action creates a pattern from either the library or
// its instrument card. Creating a first pattern must not require another one.
func (s *studioApp) patternCreation(ctx *server.Context, v workspace, csrf, trackID string) gosx.Node {
	var tracks []project.Track
	for _, track := range v.Project.Tracks {
		if kind := patternKindForTrack(v.Project, track); kind != "" {
			tracks = append(tracks, track)
		}
	}
	if len(tracks) == 0 {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Add an instrument or drum track to create a pattern."))
	}
	values := map[string]string{"newName": availablePatternName(v.Project, trackID), "track": trackID, "scene": "", "length": "16", "starter": "empty"}
	failed := false
	if state := action.States(ctx.Request)["pattern"]; !state.OK() && state.Value("action") == "create" && state.Value("createFor") == trackID {
		failed = true
		for key := range values {
			values[key] = state.Value(key)
		}
	}
	var fields []gosx.Node
	fields = append(fields, hidden("action", "create"), hidden("createFor", trackID))
	if trackID != "" {
		fields = append(fields, hidden("track", trackID), gosx.El("p", gosx.Text("For track "+trackID)))
	} else {
		var options []gosx.Node
		for _, track := range tracks {
			options = append(options, gosx.El("option", gosx.Attrs(gosx.Attr("value", track.ID), gosx.Attr("selected", track.ID == values["track"])), gosx.Text(track.ID+" · "+patternKindForTrack(v.Project, track))))
		}
		fields = append(fields, field("Track", gosx.El("select", gosx.Attrs(gosx.Attr("name", "track")), gosx.Fragment(options...))))
	}
	var scenes []gosx.Node
	scenes = append(scenes, gosx.El("option", gosx.Attrs(gosx.Attr("value", ""), gosx.Attr("selected", values["scene"] == "")), gosx.Text("Library only")))
	for _, scene := range v.Project.Scenes {
		scenes = append(scenes, gosx.El("option", gosx.Attrs(gosx.Attr("value", scene.ID), gosx.Attr("selected", values["scene"] == scene.ID)), gosx.Text(scene.ID)))
	}
	var starters []gosx.Node
	for _, choice := range []struct{ value, label string }{{"empty", "Empty grid"}, {"pulse", "Pulse"}, {"arpeggio", "Arpeggio"}, {"chords", "Held chords"}, {"backbeat", "Backbeat"}} {
		if !patternStarterAvailable(v.Project, trackID, choice.value) {
			continue
		}
		starters = append(starters, gosx.El("option", gosx.Attrs(gosx.Attr("value", choice.value), gosx.Attr("selected", choice.value == values["starter"])), gosx.Text(choice.label)))
	}
	fields = append(fields, field("Pattern name", textInput("newName", values["newName"])), field("Start with", gosx.El("select", gosx.Attrs(gosx.Attr("name", "starter")), gosx.Fragment(starters...))), field("Steps", numberInput("length", values["length"], "1", "64")), field("Assign to scene", gosx.El("select", gosx.Attrs(gosx.Attr("name", "scene")), gosx.Fragment(scenes...))), submit("", "", "Create pattern"))
	form := ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: ctx.Request.URL.RequestURI(), Class: "action-form"}, fields...)
	label := "Create pattern"
	if trackID != "" {
		label += " for " + trackID
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management"), gosx.Attr("data-gosx-key", "create-pattern-"+trackID), gosx.Attr("aria-label", label), gosx.Attr("open", failed)), gosx.El("summary", gosx.Text("Create pattern")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), form), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Start empty or with an editable idea in your song’s key and the sound’s register. Creating and assigning together has one Undo step.")))
}

func patternStarterAvailable(p *project.Project, trackID, starter string) bool {
	if trackID == "" || starter == "empty" || starter == "pulse" {
		return true
	}
	for _, track := range p.Tracks {
		if track.ID != trackID {
			continue
		}
		kind := patternKindForTrack(p, track)
		if starter == "backbeat" {
			return kind == "drums"
		}
		if kind == "drums" || kind == "" {
			return false
		}
		if starter == "arpeggio" {
			return true
		}
		if kind != "notes" {
			return false
		}
		for _, instrument := range p.Instruments {
			if instrument.ID == track.Kind {
				return instrument.Mode != "mono"
			}
		}
		return true
	}
	return false
}
