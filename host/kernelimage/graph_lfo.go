package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

// GraphLFOFlag identifies per-sample graph LFOs; no appendix is needed.
const GraphLFOFlag uint32 = 1 << 6

func hasGraphLFO(cfg *engine.Config) bool {
	for i := 0; i < cfg.Tracks; i++ {
		track := &cfg.Track[i]
		if (track.Kind == engine.VoiceGraph || track.Kind == engine.VoiceGraphPoly) && graph.HasLFO(&track.Graph) {
			return true
		}
	}
	return false
}
