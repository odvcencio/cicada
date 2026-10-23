package main

import (
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) placementVariation(ctx *server.Context, v workspace, csrf string, placement project.Placement) gosx.Node {
	for _, pattern := range v.Project.Patterns {
		if pattern.ID != placement.Content {
			continue
		}
		name := nextPatternName(v.Project, strings.ReplaceAll(pattern.ID, ".", "-"))
		message := ""
		if ctx != nil {
			state := action.States(ctx.Request)["arrange"]
			if !state.OK() && state.Value("action") == "vary" && state.Value("name") == placement.ID {
				name, message = state.Value("newName"), state.Message()
			}
		}
		return gosx.El("section", gosx.Attrs(gosx.Attr("class", "placement-variation"), gosx.Attr("data-placement-variation", placement.ID), gosx.Attr("data-gosx-key", "placement-variation-"+placement.ID)),
			gosx.El("h3", gosx.Text("Vary this phrase")),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Create an editable local pattern for this block. Other blocks keep their original pattern. A sound check preserves the song before you start editing. Undo restores the pattern and block together.")),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), s.placementForm(v, csrf, placement.ID, hidden("action", "vary"), hidden("name", placement.ID), field("New pattern name", textInput("newName", name)), submit("", "", "Make independent variation"), gosx.El("p", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status")), gosx.Text(message)))))
	}
	return gosx.Fragment()
}
