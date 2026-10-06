package engine

import (
	"math"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

// setAuthoredLayerMask records a mask chosen by a command or a scene. The mask in
// force is always the authored mask gated by the macro layer tables, so a timed
// OpSetLayerMask cannot switch on a track that a macro layer excludes.
func (e *Engine) setAuthoredLayerMask(mask uint32) {
	e.layerAuthored = mask
	e.composeLayerMask()
}

// composeLayerMask sets the effective mask from the authored mask and the macros.
func (e *Engine) composeLayerMask() {
	mask := e.layerAuthored
	if macro, active := e.macroLayerMask(); active {
		mask &= macro
	}
	e.layerMask = mask
}

// macroLayerMask combines every configured macro layer table: a track sounds
// only when every configured macro allows it. The bool is false when no macro
// layer table is active, so authored layer masks stay in charge.
func (e *Engine) macroLayerMask() (uint32, bool) {
	mask, active := uint32(1)<<e.tracks-1, false
	for id := range e.macroLayerEnabled {
		if e.macroLayerEnabled[id] && e.macroLayerInitialized[id] {
			mask &= e.macroLayerMasks[id][e.macroLayerLevel[id]]
			active = true
		}
	}
	return mask, active
}

// reapplyMacroLayers recomposes the mask after a seek or a scene reconstruction.
func (e *Engine) reapplyMacroLayers() { e.composeLayerMask() }

// processBarBoundary runs after same-tick commands so scene launches and
// conductor changes share the transport's exact bar-line ordering.
func (e *Engine) processBarBoundary() {
	if !e.transport.Playing() {
		return
	}
	tick := e.transport.Tick()
	if tick%seq.TicksPerBar != 0 || tick == e.lastBarTick {
		return
	}
	e.lastBarTick = tick
	completedBars := tick / seq.TicksPerBar
	bar := uint32(completedBars + 1)
	if e.liveEvents {
		e.emit(cmd.Message{Kind: cmd.Bar, B: bar, Tick: tick})
	}

	var changed [16]bool
	for id := range e.macroLayerEnabled {
		if !e.macroLayerEnabled[id] {
			continue
		}
		value := uint32(math.Round(float64(e.macroCurrent[id]) * 255))
		target := uint8(0)
		for level := uint8(0); level < e.macroLayerThresholdCount[id]; level++ {
			if value >= uint32(e.macroLayerThresholds[id][level]) {
				target++
			}
		}
		// The conductor exposes exactly four masks for levels 0 through 3.
		if target > 3 {
			target = 3
		}

		level := e.macroLayerLevel[id]
		if target > level {
			level++
			e.macroLayerQuiet[id] = 0
		} else if target < level {
			e.macroLayerQuiet[id]++
			if e.macroLayerQuiet[id] >= e.macroLayerRelease[id] {
				level--
				e.macroLayerQuiet[id] = 0
			}
		} else {
			e.macroLayerQuiet[id] = 0
		}
		if level != e.macroLayerLevel[id] || !e.macroLayerInitialized[id] {
			e.macroLayerLevel[id] = level
			changed[id] = true
		}
		e.macroLayerInitialized[id] = true
	}

	// Apply the combined mask once per bar, even when no level changed, so a seek or a
	// scene reconstruction cannot leave the authored mask in place.
	if _, active := e.macroLayerMask(); active {
		e.composeLayerMask()
		mask := e.layerMask
		for id, was := range changed {
			if was {
				e.emit(cmd.Message{Kind: cmd.LayerChanged, Track: uint8(id), A: uint16(e.macroLayerLevel[id]), B: mask, Tick: tick})
			}
		}
	}

	if e.phraseBars != 0 && completedBars > 0 &&
		(completedBars-1)%int64(e.phraseBars) == int64(e.phraseBars-1) {
		phrase := uint32((completedBars-1)/int64(e.phraseBars) + 1)
		e.emit(cmd.Message{Kind: cmd.PhraseEnd, B: phrase, Tick: tick})
	}
}
