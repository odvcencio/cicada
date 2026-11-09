package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/graph"
)

// The registry rows instrument.p1 to instrument.p8 are slots for authored
// instrument parameters: a track's i-th live parameter uses slot i. They are
// track-scoped like any other row, so scene settings, automation lanes,
// modulation routes and macros reach them through setParamMode.
const (
	liveParamFirst = kernel.ParamInstrumentP1
	liveParamCount = liveParamSlots // 8, or 0 in the core kernel
)

// LiveParam binds one slot of a track to the Constant node of the track's
// graph that holds an authored parameter. A project lists only the parameters
// something moves, so a track without one renders as before.
type LiveParam struct {
	Track uint8
	Slot  uint8 // 0 for instrument.p1
	Node  uint8 // the parameter's Constant node in TrackConfig.Graph
}

// liveParam is one bound slot of a voice: the node it drives, the value the
// node holds now, and the value it glides toward.
type liveParam struct {
	bound       bool
	node        uint8
	cur, target float32
}

func isLiveParam(id kernel.ParamID) bool {
	return modulationEnabled && id >= liveParamFirst && int(id-liveParamFirst) < liveParamCount
}

// IsLiveParam reports whether a registry ID is an instrument parameter slot.
func IsLiveParam(id kernel.ParamID) bool { return isLiveParam(id) }

// initLiveParams checks a track's bindings and takes each parameter's authored
// value from its node.
func (v *voiceSlot) initLiveParams(track int, spec *TrackConfig, bindings []LiveParam) error {
	for _, b := range bindings {
		if int(b.Track) != track {
			continue
		}
		if spec.Kind != VoiceGraph && spec.Kind != VoiceGraphPoly {
			return engineError("live parameters need an authored instrument track")
		}
		if int(b.Slot) >= liveParamCount || b.Node >= spec.Graph.Len || spec.Graph.Nodes[b.Node].Op != graph.Constant || v.live[b.Slot].bound {
			return engineError("invalid live instrument parameter binding")
		}
		value := spec.Graph.Nodes[b.Node].Value
		v.live[b.Slot] = liveParam{bound: true, node: b.Node, cur: value, target: value}
	}
	return nil
}

// checkLiveParams refuses a binding that no authored instrument track took:
// one for a missing track, a track of another kind, or any binding at all in
// the core kernel.
func (e *Engine) checkLiveParams(bindings []LiveParam) error {
	if len(bindings) == 0 {
		return nil
	}
	if !modulationEnabled {
		return engineError("live instrument parameters are not available in this kernel")
	}
	for _, b := range bindings {
		if int(b.Track) >= e.tracks || int(b.Slot) >= liveParamCount || !e.voices[b.Track].live[b.Slot].bound {
			return engineError("live parameter binding names no authored instrument track")
		}
	}
	return nil
}

// setLiveParam sets a slot's target. The node glides there over the row's
// smoothing time unless the change is immediate, as a seek's is.
func (e *Engine) setLiveParam(v *voiceSlot, id kernel.ParamID, value float32, immediate bool) {
	slot := int(id - liveParamFirst)
	p := &v.live[slot]
	if !p.bound {
		e.fault(FaultParam)
		return
	}
	p.target = value
	if immediate {
		p.cur = value
		v.setGraphConstant(p.node, value)
	} else if p.cur != value {
		v.liveMoving = true
	}
	v.liveChanged |= 1 << slot
}

// stepLiveParams moves each gliding slot one sample toward its target. The
// track's own render calls it, so parallel rendering sees the same values.
func (e *Engine) stepLiveParams(v *voiceSlot) {
	moving := false
	for i := range v.live {
		p := &v.live[i]
		if !p.bound || p.cur == p.target {
			continue
		}
		next := p.cur + (p.target-p.cur)*e.paramAlpha[liveParamFirst+kernel.ParamID(i)]
		if math.Abs(float64(next-p.target)) <= 1e-6*math.Max(1, math.Abs(float64(p.target))) || next == p.cur {
			next = p.target
		} else {
			moving = true
		}
		p.cur = next
		v.setGraphConstant(p.node, next)
	}
	v.liveMoving = moving
}

// restoreLiveParams returns every slot commanded since load to its saved
// value at once.
func (v *voiceSlot) restoreLiveParams(saved *[liveParamCount]float32) {
	for i := range v.live {
		if v.liveChanged&(1<<i) == 0 || !v.live[i].bound {
			continue
		}
		v.live[i].cur, v.live[i].target = saved[i], saved[i]
		v.setGraphConstant(v.live[i].node, saved[i])
	}
	v.liveChanged, v.liveMoving = 0, false
}

// liveTargets is the value each slot is heading for.
func (v *voiceSlot) liveTargets() (out [liveParamCount]float32) {
	for i := range v.live {
		out[i] = v.live[i].target
	}
	return out
}

func (v *voiceSlot) setGraphConstant(node uint8, value float32) {
	switch {
	case v.poly != nil:
		v.poly.SetConstant(int(node), value)
	case v.legacyPoly != nil:
		v.legacyPoly.SetConstant(int(node), value)
	case v.graph != nil:
		v.graph.SetConstant(int(node), value)
	}
}
