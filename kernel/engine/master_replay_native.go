//go:build !tinygo && !js

package engine

// masterInputTapEnabled records TapFrame.MasterInput. The core TinyGo and
// browser builds compile the store out.
const masterInputTapEnabled = true

// RenderMasterWithTaps runs only the master stage over a stored master input:
// inL and inR hold, frame by frame, the TapFrame.MasterInput values that a full
// render recorded. The master gain, the master processor, the export DC
// correction and the safety limiter process them with the same code a full
// render runs, so an engine built from the same configuration, given the same
// frames in the same order from its first frame on, produces the same output
// and the same PreMaster, MasterLimiterReductionDB and LimiterReductionDB taps.
// Tracks, returns, buses and meters other than the master's are not touched
// and their taps stay zero. Host builds only.
func (e *Engine) RenderMasterWithTaps(inL, inR, outL, outR []float32, taps []TapFrame) {
	n := len(outL)
	if n < 1 || n > e.maxBlock || len(outR) != n || len(inL) != n || len(inR) != n || len(taps) != n {
		clear(outL)
		clear(outR)
		e.fault(FaultRenderBlock)
		return
	}
	clear(outL)
	clear(outR)
	clear(taps)
	if e.faulted {
		return
	}
	e.taps = taps
	for frame := range n {
		if _, ok := e.renderMasterFrame(frame, inL[frame], inR[frame], outL, outR); !ok {
			break
		}
	}
	e.taps = nil
}
