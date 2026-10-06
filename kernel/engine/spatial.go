package engine

import (
	"math"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/spatial"
)

type spatialTrack struct {
	position     spatial.Vec3
	coefficients spatial.BFormat
	enabled      bool
	gain, target float32
}

type spatialState struct {
	tracks     [16]spatialTrack
	listener   spatial.Vec3
	rotation   spatial.Rotation
	music, sfx spatial.Decoder
	active     bool
	taps       []spatial.Frame
}

// RenderWithBFormat also writes listener-relative ACN/SN3D buses into
// caller-owned storage. Ordinary stereo tracks and effect returns are absent
// from these buses. Like Render, call this only from the audio-thread owner.
func (e *Engine) RenderWithBFormat(left, right []float32, buses []spatial.Frame) {
	clear(buses)
	if len(buses) != len(left) {
		clear(left)
		clear(right)
		e.fault(1)
		return
	}
	e.spatial.taps = buses
	e.Render(left, right)
	e.spatial.taps = nil
}

func (e *Engine) applySpatial(c cmd.Command) {
	point := spatial.Vec3{X: math.Float32frombits(c.Arg0), Y: math.Float32frombits(c.Arg1), Z: math.Float32frombits(c.Pad)}
	switch c.Op {
	case cmd.OpSetTrackPosition:
		t := &e.spatial.tracks[c.Track]
		if !t.enabled && c.Index == 1 {
			t.gain = t.target
		}
		t.position, t.enabled = point, c.Index == 1
		t.coefficients = spatial.Coefficients(point, e.spatial.listener)
		e.spatial.active = true
	case cmd.OpSetListenerPosition:
		e.spatial.listener = point
		for i := 0; i < e.tracks; i++ {
			t := &e.spatial.tracks[i]
			t.coefficients = spatial.Coefficients(t.position, point)
		}
	case cmd.OpSetListenerRotation:
		e.spatial.rotation = spatial.ListenerRotation(point.X, point.Y, point.Z)
	}
}
