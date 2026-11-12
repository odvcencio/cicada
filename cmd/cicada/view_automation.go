package main

import (
	"fmt"
	"math"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type viewAutomationPoint struct {
	X, Y  float64
	Label string
}
type viewAutomationLane struct {
	Path, Unit, Low, High, Curve, Description string
	EndBar                                    int64
	Points                                    []viewAutomationPoint
}

func automationViews(p *project.Project, end int64) []viewAutomationLane {
	var views []viewAutomationLane
	if end <= 0 {
		return views
	}
	endBar, _ := p.MeterMap().BarAt(end)
	for _, lane := range p.Automation {
		resolved, err := project.ResolveParameterPath(p, lane.Path)
		if err != nil || len(lane.Points) == 0 {
			continue
		}
		low, high := project.AutomationPointValue(lane.Points[0]), project.AutomationPointValue(lane.Points[0])
		for _, point := range lane.Points {
			low = math.Min(low, project.AutomationPointValue(point))
			high = math.Max(high, project.AutomationPointValue(point))
		}
		control := func(value float64) float64 {
			if resolved.Descriptor.Curve == "log" {
				return math.Log(value)
			}
			return value
		}
		bottom, top := control(low), control(high)
		y := func(value float64) float64 {
			if top == bottom {
				return 64
			}
			return 112 - 96*(control(value)-bottom)/(top-bottom)
		}
		x := func(tick int64) float64 { return 16 + 968*float64(tick)/float64(end) }
		unit := resolved.Descriptor.Unit
		if unit == "unit" {
			unit = ""
		}
		v := viewAutomationLane{Path: lane.Path, Unit: unit, Low: fmt.Sprintf("%g", low), High: fmt.Sprintf("%g", high), EndBar: endBar + 1}
		var path strings.Builder
		start := lane.Points[0].Tick
		value, _ := project.AutomationValue(p, lane, start)
		fmt.Fprintf(&path, "M %.3f %.3f", x(start), y(value))
		for i := 1; i < len(lane.Points); i++ {
			from, to := lane.Points[i-1], lane.Points[i]
			if to.Shape == "step" {
				fmt.Fprintf(&path, " L %.3f %.3f L %.3f %.3f", x(to.Tick), y(project.AutomationPointValue(from)), x(to.Tick), y(project.AutomationPointValue(to)))
				continue
			}
			for sample := 1; sample <= 64; sample++ {
				tick := from.Tick + (to.Tick-from.Tick)*int64(sample)/64
				value, _ := project.AutomationValue(p, lane, tick)
				fmt.Fprintf(&path, " L %.3f %.3f", x(tick), y(value))
			}
		}
		last := lane.Points[len(lane.Points)-1]
		if last.Tick < end {
			fmt.Fprintf(&path, " L %.3f %.3f", x(end), y(project.AutomationPointValue(last)))
		}
		v.Curve = path.String()
		var description strings.Builder
		for _, point := range lane.Points {
			label := fmt.Sprintf("%s: %g%s, %s", notation.TickPosition(p.MeterMap(), point.Tick), project.AutomationPointValue(point), unit, point.Shape)
			if point.Value.Unit == "db" || point.Value.Unit == "enum" {
				label = fmt.Sprintf("%s: %s, %s", notation.TickPosition(p.MeterMap(), point.Tick), project.AutomationPointText(point), point.Shape)
			}
			v.Points = append(v.Points, viewAutomationPoint{X: x(point.Tick), Y: y(project.AutomationPointValue(point)), Label: label})
			if description.Len() > 0 {
				description.WriteString("; ")
			}
			description.WriteString(label)
		}
		v.Description = description.String()
		views = append(views, v)
	}
	return views
}
