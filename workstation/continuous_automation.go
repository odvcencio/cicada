package main

import (
	"fmt"
	"math"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
)

func continuousAutomation(p *project.Project) gosx.Node {
	if p == nil || len(p.Automation) == 0 {
		return gosx.Fragment()
	}
	var end int64
	for _, entry := range p.Song {
		end += int64(entry.Bars) * 3840
	}
	if end == 0 {
		return gosx.Fragment()
	}
	var figures []gosx.Node
	for _, lane := range p.Automation {
		resolved, err := project.ResolveParameterPath(p, lane.Path)
		if err != nil || len(lane.Points) == 0 {
			continue
		}
		low, high := *lane.Points[0].Value.Number, *lane.Points[0].Value.Number
		for _, point := range lane.Points {
			low, high = math.Min(low, *point.Value.Number), math.Max(high, *point.Value.Number)
		}
		control := func(value float64) float64 {
			if resolved.Descriptor.Curve == "log" {
				return math.Log(value)
			}
			return value
		}
		bottom, top := control(low), control(high)
		x := func(tick int64) float64 { return 16 + 968*float64(tick)/float64(end) }
		y := func(value float64) float64 {
			if bottom == top {
				return 64
			}
			return 112 - 96*(control(value)-bottom)/(top-bottom)
		}
		var curve strings.Builder
		first := lane.Points[0]
		fmt.Fprintf(&curve, "M %.3f %.3f", x(first.Tick), y(*first.Value.Number))
		for i := 1; i < len(lane.Points); i++ {
			from, to := lane.Points[i-1], lane.Points[i]
			if to.Shape == "step" {
				fmt.Fprintf(&curve, " L %.3f %.3f L %.3f %.3f", x(to.Tick), y(*from.Value.Number), x(to.Tick), y(*to.Value.Number))
				continue
			}
			for sample := int64(1); sample <= 64; sample++ {
				tick := from.Tick + (to.Tick-from.Tick)*sample/64
				value, _ := project.AutomationValue(p, lane, tick)
				fmt.Fprintf(&curve, " L %.3f %.3f", x(tick), y(value))
			}
		}
		last := lane.Points[len(lane.Points)-1]
		if last.Tick < end {
			fmt.Fprintf(&curve, " L %.3f %.3f", x(end), y(*last.Value.Number))
		}
		unit := automationUnit(resolved.Descriptor.Unit)
		var labels []string
		var points []gosx.Node
		for _, point := range lane.Points {
			label := fmt.Sprintf("%s: %g%s, %s", notation.TickPosition(point.Tick), *point.Value.Number, unit, point.Shape)
			labels = append(labels, label)
			points = append(points, gosx.El("path", gosx.Attrs(gosx.Attr("class", "continuous-point"), gosx.Attr("d", fmt.Sprintf("M %.3f %.3fh0.001", x(point.Tick), y(*point.Value.Number)))), gosx.El("title", gosx.Text(label))))
		}
		figures = append(figures, gosx.El("figure", gosx.Attrs(gosx.Attr("class", "continuous-lane"), gosx.Attr("data-continuous-path", lane.Path)),
			gosx.El("figcaption", gosx.El("strong", gosx.Text(lane.Path)), gosx.El("span", gosx.Text(fmt.Sprintf("%d points", len(lane.Points))))),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "continuous-scale")), gosx.El("span", gosx.Text(fmt.Sprintf("%g%s", high, unit))), gosx.El("span", gosx.Text(fmt.Sprintf("%g%s", low, unit)))),
			gosx.El("svg", gosx.Attrs(gosx.Attr("viewBox", "0 0 1000 128"), gosx.Attr("preserveAspectRatio", "none"), gosx.Attr("role", "img"), gosx.Attr("aria-label", lane.Path+" automation")),
				gosx.El("desc", gosx.Text(strings.Join(labels, "; "))),
				gosx.El("path", gosx.Attrs(gosx.Attr("class", "continuous-grid"), gosx.Attr("d", "M16 16H984 M16 64H984 M16 112H984"))),
				gosx.El("path", gosx.Attrs(gosx.Attr("class", "continuous-curve"), gosx.Attr("d", curve.String()))), gosx.Fragment(points...)),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "continuous-time")), gosx.El("span", gosx.Text("Bar 1")), gosx.El("span", gosx.Text(fmt.Sprintf("Bar %d", end/3840+1))))))
	}
	return ui.Panel(ui.PanelProps{ID: "continuous-automation", Title: "Continuous automation", Description: "Edit automate blocks in the score to change these curves."}, gosx.Fragment(figures...))
}
