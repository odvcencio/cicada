package engine

import "m31labs.dev/cicada/kernel/fx"

type Stereo struct{ Left, Right float32 }

// TapFrame records post-fader tracks, returns, buses before compression and
// master processing, and pre-limiter master samples. The host owns storage.
type TapFrame struct {
	Tracks                                  [MaxTracks]Stereo
	ReturnA, ReturnB, Music, SFX, PreMaster Stereo
	LimiterReductionDB                      float64
}

func (e *Engine) RenderWithTaps(left, right []float32, taps []TapFrame) {
	if len(taps) != len(left) {
		e.fault(FaultRenderBlock)
		clear(left)
		clear(right)
		return
	}
	clear(taps)
	e.taps = taps
	e.Render(left, right)
	e.taps = nil
}

func (e *Engine) LatencyFrames() int {
	return e.limiter.LatencyFrames() + e.TrackLatencyFrames() + e.MasterProcessorLatencyFrames()
}

func (e *Engine) TrackLatencyFrames() int {
	frames := 0
	for i := 0; i < e.tracks; i++ {
		v := &e.voices[i]
		n := 0
		if v.insert != nil {
			n = fx.DriveLatencyFrames
		}
		if v.chain != nil {
			n += v.chain.latency
		}
		frames = max(frames, n)
	}
	return frames
}
