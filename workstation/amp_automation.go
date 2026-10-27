package main

import (
	"net/url"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

// Amp controls are track-owned; the catalog exposes them only when the
// playback track has one unambiguous amp insert. A shared declaration is safe
// to automate because this does not edit the declaration itself.
func ampAutomationLinks(ctx *server.Context, p *project.Project, track string) gosx.Node {
	if p == nil || p.Edition < 2 || track == "" {
		return gosx.Fragment()
	}
	var links []gosx.Node
	for _, a := range project.ParamAddresses(p) {
		if a.Track != track || !strings.HasPrefix(a.Param, "amp.") {
			continue
		}
		resolved, err := project.ResolveParameterPath(p, a.Address)
		if err != nil || !resolved.Descriptor.Live || !resolved.Descriptor.Automatable || resolved.Descriptor.Unit == "enum" || resolved.Descriptor.Curve == "toggle" {
			continue
		}
		label := strings.TrimPrefix(a.Param, "amp.")
		if label == "" {
			continue
		}
		label = strings.ToUpper(label[:1]) + label[1:]
		q := url.Values{"curvePath": {a.Address}}
		target := "/?panel=session&" + q.Encode() + "#automation-editor"
		if ctx.Request.URL.Path == "/studio" {
			q = ctx.Request.URL.Query()
			q.Set("dock", "automation")
			q.Set("curvePath", a.Address)
			target = "/studio?" + q.Encode() + "#automation-editor"
		}
		links = append(links, gosx.El("a", gosx.Attrs(gosx.Attr("href", target), gosx.Attr("data-amp-automation-link", "")), gosx.Text("Automate "+label)))
	}
	if len(links) == 0 {
		return gosx.Fragment()
	}
	var bypassNotice gosx.Node = gosx.Fragment()
	for _, t := range p.Tracks {
		if t.ID != track {
			continue
		}
		inserts := append([]string(nil), t.Mixer.Inserts...)
		inserts = append(inserts, strings.Fields(strings.ReplaceAll(t.Mixer.Insert, "->", " "))...)
		for _, name := range inserts {
			for _, fx := range p.Effects {
				if fx.ID == name && fx.Kind == "ampcab" {
					bypass := fx.Params["bypass"]
					if bypass.Text == "on" || bypass.Text == "true" || bypass.Number != nil && *bypass.Number != 0 {
						bypassNotice = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("This playback amp is bypassed; enable it to hear these changes."))
					}
				}
			}
		}
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "amp-automation")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Automate this track's playback amp. Input monitoring uses separate settings.")),
		bypassNotice,
		gosx.El("nav", gosx.Attrs(gosx.Attr("class", "actions"), gosx.Attr("aria-label", "Playback amp automation for "+track)), gosx.Fragment(links...)))
}
