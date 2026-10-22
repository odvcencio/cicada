package main

import (
	"fmt"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/gosx"
)

func (s *studioApp) sceneTakes(v workspace, csrf, chosen string, takes []takejournal.Take, active bool) gosx.Node {
	var rows []gosx.Node
	for _, take := range takes {
		if take.Timeline != nil || chosen != "" && take.Track != chosen || take.Frames == 0 || take.Asset.Path == "" {
			continue
		}
		label := fmt.Sprintf("Scene take %d", len(rows)+1)
		length := fmt.Sprintf("%d frames", take.Frames)
		if take.Rate > 0 {
			length = fmt.Sprintf("%.2f seconds", float64(take.Frames)/float64(take.Rate))
		}
		available, selected, trackExists := false, false, false
		for _, track := range v.Project.Tracks {
			trackExists = trackExists || track.ID == take.Track && track.Kind == "audio"
		}
		for _, scene := range v.Project.Scenes {
			if scene.ID == take.Scene {
				available = true
				selected = scene.Bindings[take.Track] == take.ID+"-clip"
			}
		}
		message := "Use in " + take.Scene
		if selected {
			message = "In " + take.Scene
		}
		if !available {
			message = "Scene no longer exists"
		}
		if !trackExists {
			message = "Audio track no longer exists"
		}
		status := ""
		if take.Incomplete {
			status = " · incomplete: preview before using"
		}
		rows = append(rows, gosx.El("li", gosx.Attrs(gosx.Attr("class", "strip"), gosx.Attr("data-scene-take", take.ID)),
			gosx.El("p", gosx.El("strong", gosx.Text(label)), gosx.Text(" · "+take.Track+" · "+take.Scene+" · "+length+status)),
			gosx.El("audio", gosx.Attrs(gosx.BoolAttr("controls"), gosx.Attr("preload", "none"), gosx.Attr("aria-label", "Preview "+label), gosx.Attr("src", takePreviewURL(take.ID, v.Revision, 60, 60, false)))),
			s.form(v, csrf, "session", "take", hidden("takeId", take.ID), gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", "select"), gosx.Attr("disabled", active || selected || !available || !trackExists)), gosx.Text(message)))))
	}
	if len(rows) == 0 {
		return gosx.Fragment()
	}
	return gosx.El("section", gosx.Attrs(gosx.Attr("id", "scene-takes")),
		gosx.El("h3", gosx.Text("Scene takes")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("These passes were recorded for a scene. Choose a take to use in that scene. Original recordings stay unchanged.")),
		gosx.El("ul", gosx.Attrs(gosx.Attr("class", "take-list")), gosx.Fragment(rows...)))
}
