package project

import (
	"strings"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
)

// BuiltinDrumLanes keeps the six legacy voices. An added lane needs an
// authored parameter or a hit in a pattern bound to this track in any scene.
// Rest-only rows do not add voices when source is printed and parsed again.
func BuiltinDrumLanes(score *notation.Score, track notation.Track) [drum.LaneCount]bool {
	lanes := legacyDrumLanes()
	for _, param := range track.Params {
		selectDrumParamLane(&lanes, param.Name)
	}
	used := map[string]bool{}
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Track == track.Name && binding.Pattern != "off" && binding.Pattern != "keep" {
				used[binding.Pattern] = true
			}
		}
	}
	for _, pattern := range score.Patterns {
		if pattern.Kind != "drums" || !used[pattern.Name] {
			continue
		}
		for _, row := range pattern.Lanes {
			lane, ok := drumLane(row.Name)
			if !ok {
				continue
			}
			for _, step := range row.Hits {
				if step.Text != "." {
					lanes[lane] = true
				}
			}
		}
	}
	return lanes
}

func projectDrumLanes(p *Project, track Track) [drum.LaneCount]bool {
	lanes := legacyDrumLanes()
	for name := range track.Params {
		selectDrumParamLane(&lanes, name)
	}
	used := map[string]bool{}
	for _, id := range track.Slots {
		if id != nil {
			used[*id] = true
		}
	}
	for _, pattern := range p.Patterns {
		if pattern.Kind != "drums" || !used[pattern.ID] {
			continue
		}
		for lane, name := range drum.Names {
			for _, step := range pattern.Lanes[name] {
				if step != nil {
					lanes[lane] = true
				}
			}
		}
	}
	return lanes
}

func legacyDrumLanes() [drum.LaneCount]bool {
	var lanes [drum.LaneCount]bool
	for lane := drum.BD; lane < drum.LT; lane++ {
		lanes[lane] = true
	}
	return lanes
}

func selectDrumParamLane(lanes *[drum.LaneCount]bool, name string) {
	prefix, _, ok := strings.Cut(name, "_")
	if lane, known := drumLane(prefix); ok && known {
		lanes[lane] = true
	}
}

func drumVoiceCount(lanes [drum.LaneCount]bool) int {
	count := 0
	for _, enabled := range lanes {
		if enabled {
			count++
		}
	}
	return count
}
