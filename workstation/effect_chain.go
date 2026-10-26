package main

import (
	"strconv"
	"strings"

	"m31labs.dev/gosx"
)

func (s *studioApp) effectChain(v workspace, csrf string, strip mixerStrip) gosx.Node {
	var stages []gosx.Node
	for i, name := range strip.Inserts {
		form := s.form(v, csrf, "mixer", "mixer", hidden("action", "chain-edit"), hidden("track", strip.ID), hidden("chain", strings.Join(strip.Inserts, " ")), hidden("index", strconv.Itoa(i)),
			gosx.El("a", gosx.Attrs(gosx.Attr("href", "#effect-"+name)), gosx.Text(name)),
			gosx.El("button", gosx.Attrs(gosx.Attr("name", "operation"), gosx.Attr("value", "up"), gosx.Attr("aria-label", "Move "+name+" earlier"), gosx.Attr("disabled", i == 0)), gosx.Text("↑")),
			gosx.El("button", gosx.Attrs(gosx.Attr("name", "operation"), gosx.Attr("value", "down"), gosx.Attr("aria-label", "Move "+name+" later"), gosx.Attr("disabled", i == len(strip.Inserts)-1)), gosx.Text("↓")),
			gosx.El("button", gosx.Attrs(gosx.Attr("name", "operation"), gosx.Attr("value", "remove"), gosx.Attr("aria-label", "Remove "+name)), gosx.Text("Remove")))
		stages = append(stages, gosx.El("li", form))
	}
	var names []string
	if v.Project != nil {
		for _, fx := range v.Project.Effects {
			if fx.Kind == "drive" || fx.Kind == "eq" || fx.Kind == "comp" || fx.Kind == "deesser" || fx.Kind == "ampcab" {
				names = append(names, fx.ID)
			}
		}
	}
	var add gosx.Node = gosx.Fragment()
	if len(names) > 0 && strip.ID != "music" && strip.ID != "sfx" && strip.ID != "master" {
		add = s.form(v, csrf, "mixer", "mixer", hidden("action", "chain-edit"), hidden("operation", "add"), hidden("track", strip.ID), hidden("chain", strings.Join(strip.Inserts, " ")), hidden("index", "0"), field("Effect to add", selectInput("effect", "", names, "Effect to add on "+strip.Name)), submit("", "", "Add effect"))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "effect-chain"), gosx.Attr("aria-label", strip.Name+" insert chain")), gosx.El("strong", gosx.Text("Insert chain")), gosx.El("ol", gosx.Fragment(stages...)), add)
}
