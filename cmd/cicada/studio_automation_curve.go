package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type studioCurvePoint struct {
	OldAt string  `json:"oldAt,omitempty"`
	At    string  `json:"at"`
	Value string  `json:"value"`
	Shape string  `json:"shape"`
	Curve float64 `json:"curve"`
}

// Edit one authored point; comments and all other declarations keep their bytes.
func curvePointSource(source []byte, path string, point studioCurvePoint, remove bool, edition int) ([]byte, error) {
	score, ds := notation.ParseEdition(source, edition)
	if score == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score must validate before editing automation")
	}
	p, ds := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(ds) {
		return nil, fmt.Errorf("score parameters must validate before editing automation")
	}
	resolved, err := project.ResolveParameterPath(p, path)
	if err != nil {
		return nil, err
	}
	d := resolved.Descriptor
	if !d.Live || !d.Automatable || d.Curve == "toggle" || d.Unit == "enum" {
		return nil, fmt.Errorf("%s has no continuous numeric automation", path)
	}
	tick, err := notation.MusicalTicks(point.At, true)
	if err != nil || tick < 0 || tick > studioArrangementEnd(p) || tick%240 != 0 {
		return nil, fmt.Errorf("choose a sixteenth-note position within the song")
	}
	old := tick
	if point.OldAt != "" {
		old, err = notation.MusicalTicks(point.OldAt, true)
		if err != nil {
			return nil, err
		}
	}
	if point.Shape == "" {
		point.Shape = "linear"
	}
	if point.Shape != "linear" && point.Shape != "step" && point.Shape != "smooth" && point.Shape != "exponential" && point.Shape != "curve" {
		return nil, fmt.Errorf("choose linear, step, smooth, exponential or curve")
	}
	if math.IsNaN(point.Curve) || math.IsInf(point.Curve, 0) || math.Abs(point.Curve) > 8 || point.Shape != "curve" && point.Curve != 0 {
		return nil, fmt.Errorf("curve tension must be −8 to +8 and only applies to curve")
	}
	literal := ""
	if !remove {
		d.DisplayStep = 0
		raw, _ := json.Marshal(point.Value)
		literal, err = canonicalMixerLiteral(d, raw)
		if err != nil {
			return nil, err
		}
	}
	text := listeningPosition(tick) + " " + literal + " " + point.Shape
	if point.Shape == "curve" {
		text += " " + strconv.FormatFloat(point.Curve, 'f', -1, 64)
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var lane *gts.Node
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) == "automate_decl" && w.Text(w.Field(n, "path")) == path {
			lane = n
			break
		}
	}
	nl := "\n"
	if bytes.Contains(source, []byte("\r\n")) {
		nl = "\r\n"
	}
	if lane == nil {
		if remove || point.OldAt != "" {
			return nil, fmt.Errorf("automation point no longer exists")
		}
		return append(append([]byte(nil), source...), []byte(nl+"automate "+path+" {"+nl+"  "+text+nl+"}"+nl)...), nil
	}
	var found *gts.Node
	count := 0
	insert := int(lane.EndByte()) - 1
	for i := 0; i < lane.NamedChildCount(); i++ {
		n := lane.NamedChild(i)
		if w.Type(n) != "automation_point" {
			continue
		}
		count++
		at, e := notation.MusicalTicks(w.Text(w.Field(n, "at")), true)
		if e != nil {
			return nil, e
		}
		if at == old {
			found = n
		}
		if at == tick && old != tick {
			return nil, fmt.Errorf("another automation point already occupies that position")
		}
		if at > tick && insert == int(lane.EndByte())-1 {
			insert = int(n.StartByte())
		}
	}
	if found != nil {
		if remove {
			if count == 1 {
				return replaceSongSpan(source, int(lane.StartByte()), int(lane.EndByte()), nil)
			}
			return replaceSongSpan(source, int(found.StartByte()), int(found.EndByte()), nil)
		}
		// Moving across neighbors is rejected by project validation; dragging in
		// Session clamps between them and keeps the lane's authored order.
		return replaceSongSpan(source, int(found.StartByte()), int(found.EndByte()), []byte(text))
	}
	if remove || point.OldAt != "" {
		return nil, fmt.Errorf("automation point no longer exists")
	}
	return replaceSongSpan(source, insert, insert, []byte("  "+text+nl))
}
