package project

import (
	"fmt"
	"m31labs.dev/cicada/notation"
	"strconv"
	"strings"
)

type Arrangement struct {
	Placements []Placement `json:"placements" cicada:"Named regions in source order"`
	Markers    []Marker    `json:"markers" cicada:"Named musical positions"`
}
type Placement struct {
	ID          string `json:"id" cicada:"Stable placement identity"`
	Track       string `json:"track" cicada:"Track identifier"`
	Content     string `json:"content" cicada:"Pattern or audio clip identifier"`
	AtTick      int64  `json:"at_tick" cicada:"Absolute start tick" unit:"tick"`
	LengthTicks int64  `json:"length_ticks" cicada:"Exclusive duration" unit:"tick"`
}
type Marker struct {
	ID     string `json:"id" cicada:"Stable marker identity"`
	AtTick int64  `json:"at_tick" cicada:"Absolute marker tick" unit:"tick"`
}

func arrangementPlacements(p *Project) []Placement {
	if p.Arrange == nil {
		return nil
	}
	return p.Arrange.Placements
}
func lowerArrangement(p *Project, s *notation.Score) {
	if s.Arrange == nil {
		return
	}
	p.Format, p.Version, p.p2Syntax = FormatID2, 2, true
	p.Arrange = &Arrangement{Placements: []Placement{}, Markers: []Marker{}}
	for _, v := range s.Arrange.Placements {
		p.Arrange.Placements = append(p.Arrange.Placements, Placement{ID: v.Name, Track: v.Track, Content: v.Content, AtTick: v.AtTick, LengthTicks: v.LengthTicks})
	}
	for _, v := range s.Arrange.Markers {
		p.Arrange.Markers = append(p.Arrange.Markers, Marker{ID: v.Name, AtTick: v.AtTick})
	}
}
func arrangementSource(a *Arrangement) string {
	var out strings.Builder
	out.WriteString("arrange {")
	for _, p := range a.Placements {
		fmt.Fprintf(&out, "\n  place %s %s %s {\n    at = %dticks\n    length = %dticks\n  }", p.ID, p.Track, p.Content, p.AtTick, p.LengthTicks)
	}
	for _, m := range a.Markers {
		fmt.Fprintf(&out, "\n  marker %s {\n    at = %dticks\n  }", m.ID, m.AtTick)
	}
	out.WriteString("\n}")
	return out.String()
}
func validateArrangementProject(p *Project) error {
	if p.Arrange == nil {
		return nil
	}
	if p.Format != FormatID2 || p.Edition != 2 || len(p.Song) != 0 {
		return fmt.Errorf("CICADA-ARRANGEMENT: arrange requires edition 2 and exclusive arrangement authority")
	}
	for _, placement := range p.Arrange.Placements {
		if !validID(placement.ID) {
			return fmt.Errorf("CICADA-ARRANGEMENT: invalid placement ID %q", placement.ID)
		}
		for _, track := range p.Tracks {
			if track.ID == placement.Track && track.Kind != "audio" && !hasSlot(track, placement.Content) {
				return fmt.Errorf("CICADA-ARRANGEMENT: placement %s content has no track slot", placement.ID)
			}
		}
	}
	if p.Arrange.Placements == nil || p.Arrange.Markers == nil {
		return fmt.Errorf("CICADA-ARRANGEMENT: placement and marker arrays must be explicit")
	}
	for _, marker := range p.Arrange.Markers {
		if !validID(marker.ID) {
			return fmt.Errorf("CICADA-ARRANGEMENT: invalid marker ID %q", marker.ID)
		}
	}
	s := &notation.Score{Version: p.Edition, Arrange: &notation.Arrangement{}}
	for _, t := range p.Tracks {
		s.Tracks = append(s.Tracks, notation.Track{Name: t.ID, Kind: t.Kind})
	}
	for _, v := range p.Patterns {
		s.Patterns = append(s.Patterns, notation.Pattern{Name: v.ID, Kind: v.Kind})
	}
	for _, v := range p.Clips {
		s.Clips = append(s.Clips, notation.Clip{Name: v.Name})
	}
	for _, v := range p.Kits {
		s.Kits = append(s.Kits, notation.Kit{Name: v.ID})
	}
	for _, v := range p.Arrange.Placements {
		s.Arrange.Placements = append(s.Arrange.Placements, notation.Placement{Name: v.ID, Track: v.Track, Content: v.Content, AtTick: v.AtTick, LengthTicks: v.LengthTicks, Params: []notation.Param{{Name: "at", Value: strconv.FormatInt(v.AtTick, 10) + "ticks"}, {Name: "length", Value: strconv.FormatInt(v.LengthTicks, 10) + "ticks"}}})
	}
	for _, v := range p.Arrange.Markers {
		s.Arrange.Markers = append(s.Arrange.Markers, notation.Marker{Name: v.ID, AtTick: v.AtTick, Params: []notation.Param{{Name: "at", Value: strconv.FormatInt(v.AtTick, 10) + "ticks"}}})
	}
	if ds := notation.ValidateArrangement(s); len(ds) > 0 {
		return fmt.Errorf("%s: %s", ds[0].Code, ds[0].Message)
	}
	return nil
}
