package engine

import (
	"math"

	"m31labs.dev/cicada/kernel/fx"
)

// MaxReturns is the number of return slots: A (the delay or tape echo), B (the
// first reverb) and C to H. Config.DelayA, Config.TapeA and Config.ReverbB
// stay slots A and B; Routing holds slots C to H.
const MaxReturns = 8

// ReturnConfig is one extra return, in slots C to H (Slot 2 to 7). Only a
// reverb can take an extra slot so far.
type ReturnConfig struct {
	// Name is the score's effect name, for messages.
	Name   string `json:",omitempty"`
	Slot   uint8
	Reverb *fx.ReverbParams
}

// SendConfig is a track's send to an extra return: a linear gain 0 to 1,
// tapped before the fader and pan when Pre is set and after them otherwise,
// as the sends to slots A and B are.
type SendConfig struct {
	Slot uint8
	Gain float64
	Pre  bool `json:",omitempty"`
}

// RoutingConfig holds the returns beyond the legacy pair and the sends to
// them. A score with one delay or tape and one reverb leaves it nil.
type RoutingConfig struct {
	// Returns are in slot order, each slot at most once.
	Returns []ReturnConfig `json:",omitempty"`
	// TrackSends lists each track's sends to extra returns, by track index;
	// it may be shorter than the track count.
	TrackSends [][]SendConfig `json:",omitempty"`
}

// extraReturn is the state of one return in slots C to H. sendL and sendR
// sum this frame's sends, as the frame loop's locals do for slots A and B.
type extraReturn struct {
	reverb       *fx.Reverb
	sendL, sendR float32
}

// trackSend is one track send to an extra return.
type trackSend struct {
	gain float32
	ret  uint8 // index into routingState.returns
	pre  bool
}

// routingState is the engine's state for extra returns. It is nil for every
// score without them, so the frame loop runs exactly the legacy operations.
type routingState struct {
	returns []extraReturn
	sends   [][]trackSend
}

//go:noinline
func (e *Engine) initRouting(cfg *Config) error {
	r := cfg.Routing
	if r == nil || len(r.Returns) == 0 && len(r.TrackSends) == 0 {
		return nil
	}
	if !returnsEnabled {
		return engineError("extra reverb returns are not available in portable kernels")
	}
	if len(r.Returns) > MaxReturns-2 || len(r.TrackSends) > cfg.Tracks {
		return engineError("routing configuration is out of range")
	}
	state := &routingState{returns: make([]extraReturn, len(r.Returns)), sends: make([][]trackSend, cfg.Tracks)}
	var bySlot [MaxReturns]int
	for i := range bySlot {
		bySlot[i] = -1
	}
	previous := uint8(1)
	for i, spec := range r.Returns {
		if spec.Slot <= previous || spec.Slot >= MaxReturns || spec.Reverb == nil {
			return engineError("extra returns need slots C to H in order, each with a reverb")
		}
		previous = spec.Slot
		reverb, err := fx.NewReverb(cfg.SampleRate)
		if err == nil {
			err = reverb.SetParams(*spec.Reverb)
		}
		if err != nil {
			return err
		}
		reverb.Reset()
		state.returns[i].reverb = reverb
		bySlot[spec.Slot] = i
	}
	for track, sends := range r.TrackSends {
		seen := uint8(0)
		for _, send := range sends {
			if send.Slot >= MaxReturns || bySlot[send.Slot] < 0 || seen&(1<<send.Slot) != 0 || math.IsNaN(send.Gain) || send.Gain < 0 || send.Gain > 1 {
				return engineError("a track send needs an extra return slot, at most once, and a gain from 0 to 1")
			}
			seen |= 1 << send.Slot
			if send.Gain > 0 {
				state.sends[track] = append(state.sends[track], trackSend{gain: float32(send.Gain), ret: uint8(bySlot[send.Slot]), pre: send.Pre})
			}
		}
	}
	e.routing = state
	return nil
}

// resetExtraReturns clears the returns in slots C to H wherever the engine
// clears the legacy returns.
func (e *Engine) resetExtraReturns() {
	if !returnsEnabled || e.routing == nil {
		return
	}
	for i := range e.routing.returns {
		r := &e.routing.returns[i]
		r.reverb.Reset()
		r.sendL, r.sendR = 0, 0
	}
}
