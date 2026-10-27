package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// loadAutomation copies a prepared, ordered parameter timeline before rendering.
// Only parameter commands are accepted; the callback never grows a queue.
func (e *Engine) loadAutomation(cfg *Config) error {
	if len(cfg.Automation) > 65535 {
		return engineError("automation exceeds 65535 control points")
	}
	for i, c := range cfg.Automation {
		if int(c.Index) >= len(kernel.Params) || !kernel.Params[c.Index].Automatable || c.Op != cmd.OpSetParam || c.Tick < 0 || i > 0 && c.Tick < cfg.Automation[i-1].Tick || int(c.Track) >= cfg.Tracks && c.Track != 0xff || c.Validate(uint8(cfg.Tracks)) != nil {
			return engineError("invalid automation timeline")
		}
		if ampControlsEnabled && c.Index >= uint16(kernel.ParamAmpDrive) && c.Index <= uint16(kernel.ParamAmpOutput) {
			if int(c.Track) >= cfg.Tracks {
				return engineError("amp automation requires a track")
			}
			count := 0
			for _, stage := range cfg.Track[c.Track].InsertChain {
				if stage.Kind == TrackEffectAmpCab {
					count++
				}
			}
			if count != 1 {
				return engineError("amp automation requires exactly one amp insert")
			}
		}
		if keysEnabled && isKeysControl(kernel.ParamID(c.Index)) && (c.Track == 0xff || !KeysControlFits(&cfg.Track[c.Track], kernel.ParamID(c.Index))) {
			return engineError("keyboard automation needs a keyboard track of its family")
		}
	}
	e.automation = append([]cmd.Command(nil), cfg.Automation...)
	e.automationLanes = e.automationLanes[:0]
	for _, c := range e.automation {
		known := false
		for _, lane := range e.automationLanes {
			known = known || lane.track == c.Track && lane.id == kernel.ParamID(c.Index)
		}
		if !known {
			e.automationLanes = append(e.automationLanes, automationLane{c.Track, kernel.ParamID(c.Index)})
		}
	}
	e.automationTick = -1
	e.automationCycle = e.scheduleDuration
	return nil
}

// advanceAutomation consumes immutable controls at their musical tick. Values
// use the same smoothing path as live controls in native and WASM rendering.
func (e *Engine) advanceAutomation() {
	if len(e.automation) == 0 {
		return
	}
	tick := e.transport.Tick()
	if e.loopSong && e.automationCycle > 0 {
		tick %= e.automationCycle
	}
	if tick == e.automationTick {
		return
	}
	// A seek, a play, a loop wrap or a replay after the song ended rewinds the
	// timeline: values the lanes would hold at this tick are rebuilt at once. The
	// first play from tick 0 is the one case that keeps the normal smoothing.
	snap := false
	if e.automationTick < 0 {
		snap = tick > 0 || e.automationIndex > 0
		e.automationIndex = 0
	} else if tick < e.automationTick {
		snap = true
		e.automationIndex = 0
	}
	for e.automationIndex < len(e.automation) && e.automation[e.automationIndex].Tick <= tick {
		c := e.automation[e.automationIndex]
		if snap {
			e.setParamImmediate(c)
		} else {
			e.setParam(c)
		}
		e.automationIndex++
	}
	e.automationTick = tick
}
