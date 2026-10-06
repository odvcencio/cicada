package engine

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// loadAutomation copies a prepared, ordered parameter timeline before rendering.
// Only parameter commands are accepted; the callback never grows a queue.
func (e *Engine) loadAutomation(cfg *Config) error {
	if len(cfg.Automation) > 65535 {
		return Error("automation exceeds 65535 control points")
	}
	for i, c := range cfg.Automation {
		if int(c.Index) >= len(kernel.Params) || !kernel.Params[c.Index].Automatable || c.Op != cmd.OpSetParam || c.Tick < 0 || i > 0 && c.Tick < cfg.Automation[i-1].Tick || int(c.Track) >= cfg.Tracks && c.Track != 0xff || c.Validate(uint8(cfg.Tracks)) != nil {
			return Error("invalid automation timeline")
		}
	}
	e.automation = append([]cmd.Command(nil), cfg.Automation...)
	e.automationTick = -1
	for _, entry := range cfg.Song {
		e.automationCycle += int64(entry.Bars) * 3840
	}
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
	snap := e.automationTick < 0 && tick > 0 || e.automationTick >= 0 && tick < e.automationTick
	if snap {
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
