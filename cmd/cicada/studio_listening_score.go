package main

import (
	"fmt"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

// Solo listening keeps the complete authored timeline. Removing placements
// would shorten its authority and invalidate later tempo or curve points.
func compileStudioListeningScore(path string, p *project.Project, listening *studioListening, rate int) (liveplay.Score, error) {
	score, err := compileLiveProjectAtRate(path, p, rate)
	if err != nil || listening == nil || listening.Kind != "placement" || listening.Mode != "solo" {
		return score, err
	}
	if p.Arrange != nil {
		for i, place := range p.Arrange.Placements {
			if place.ID == listening.Name {
				// Schedule placement identities follow the authored slice order.
				if err := score.Engine.SetPlaybackPlacement(uint32(i + 1)); err != nil {
					_ = score.Engine.Close()
					return liveplay.Score{}, err
				}
				return score, nil
			}
		}
	}
	_ = score.Engine.Close()
	return liveplay.Score{}, fmt.Errorf("listening placement %q is no longer in the score", listening.Name)
}
