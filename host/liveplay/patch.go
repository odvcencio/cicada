package liveplay

import (
	"math"

	"m31labs.dev/cicada/kernel"
)

// noteCommitted clears a matching preview after its committed value lands.
// Call it only on the render thread; the control snapshot stays immutable.
func (p *Player) noteCommitted(track uint8, id kernel.ParamID, value float32) {
	slot := int(track)
	if track == 0xff {
		slot = 16
	}
	if slot >= len(p.clearedVersions) || id >= kernel.ParamCount {
		return
	}
	snapshot := p.overrides.Load()
	if snapshot == nil {
		return
	}
	override := snapshot.values[slot][id]
	if override.active && math.Float32bits(value) == math.Float32bits(override.value) {
		p.clearedVersions[slot][id] = override.version
	}
}
