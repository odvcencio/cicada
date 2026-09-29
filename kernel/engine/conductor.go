package engine

import (
	"math"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

func (e *Engine) applyLayerMask(mask uint32) {
	e.layerMask = mask
}

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
		if level != e.macroLayerLevel[id] {
			e.macroLayerLevel[id] = level
			mask := e.macroLayerMasks[id][level]
			e.applyLayerMask(mask)
			e.emit(cmd.Message{Kind: cmd.LayerChanged, A: uint16(level), B: mask, Tick: tick})
		} else if !e.macroLayerInitialized[id] {
			e.applyLayerMask(e.macroLayerMasks[id][level])
		}
		e.macroLayerInitialized[id] = true
	}

	if e.phraseBars != 0 && completedBars > 0 &&
		(completedBars-1)%int64(e.phraseBars) == int64(e.phraseBars-1) {
		phrase := uint32((completedBars-1)/int64(e.phraseBars) + 1)
		e.emit(cmd.Message{Kind: cmd.PhraseEnd, B: phrase, Tick: tick})
	}
}
