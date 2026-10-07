package render

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// advanceRenderChains splits offline blocks at exact section boundaries. A
// scene's explicit pattern/off action disables its authored track chain.
func advanceRenderChains(tracks []trackRuntime, tick int64) error {
	for i := range tracks {
		track := &tracks[i]
		if len(track.chain) == 0 || tick < track.chainDue {
			continue
		}
		name := track.chain[track.chainIndex]
		scene := notation.Scene{Bindings: []notation.Binding{{Track: track.name, Pattern: name}}}
		chain := track.chain
		if err := applyScene(tracks[i:i+1], &scene, false); err != nil {
			return err
		}
		track.chain = chain
		track.startTick = track.chainDue
		track.chainIndex = (track.chainIndex + 1) % len(chain)
		var pattern *seq.Pattern
		if track.drums != nil {
			for _, lane := range track.activeDrums {
				if lane != nil {
					pattern = lane
					break
				}
			}
		} else {
			pattern = track.active
		}
		if pattern != nil {
			track.chainDue += int64(pattern.Len) * pattern.GridTicks()
		}
	}
	return nil
}

func nextRenderChainSample(tracks []trackRuntime, clock seq.Clock, end int64) int64 {
	for i := range tracks {
		if len(tracks[i].chain) > 0 {
			end = min(end, clock.SampleAtTick(tracks[i].chainDue))
		}
	}
	return end
}
