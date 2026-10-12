package liveplay

import (
	"math"

	"m31labs.dev/cicada/kernel"
)

// noteCommitted clears a matching preview after its committed value lands.
// Call it only on the render thread; the control snapshot stays immutable.
func (p *Player) noteCommitted(track uint8, id kernel.ParamID, value float32) {
	if id >= kernel.ParamCount {
		return
	}
	snapshot := p.overrides.Load()
	if snapshot == nil {
		return
	}
	identity := overrideTrack{}
	if track != 0xff {
		names := p.trackNames.Load()
		if names == nil || track >= names.count {
			return
		}
		identity = names.overrideTrack(track)
	}
	overrides := snapshot.tracks[identity]
	if overrides == nil {
		return
	}
	override := overrides.values[id]
	if override.active && math.Float32bits(value) == math.Float32bits(override.value) {
		overrides.state.retire(id, override.version)
	}
}

// retire runs only on the render thread. Its atomic mirror lets
// control callers observe retirement without mutating SetParam's snapshot.
func (state *overrideState) retire(id kernel.ParamID, version uint64) {
	state.clearedVersions[id] = version
	state.cancelledVersions[id] = 0
	state.retiredVersions[id].Store(version)
}
