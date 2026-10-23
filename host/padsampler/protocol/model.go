// Package protocol defines the sampler wire format without host storage or compilation.
package protocol

import (
	"fmt"
	"math"
)

const Rate = 48000
const MaxFrames = 8 << 20

type Pad struct {
	Name          string  `json:"name"`
	Source        string  `json:"source"`
	Start         int64   `json:"start"`
	End           int64   `json:"end"`
	Pitch         int     `json:"pitch"`
	GainDB        float64 `json:"gainDB"`
	Reverse       bool    `json:"reverse"`
	AttackMS      float64 `json:"attackMS"`
	ReleaseMS     float64 `json:"releaseMS"`
	Mode          string  `json:"mode"`
	Choke         int     `json:"choke"`
	BypassEffects bool    `json:"bypassEffects"`
}

type Effects struct {
	Cutoff    float64 `json:"cutoff"`
	Drive     float64 `json:"drive"`
	DelayMS   float64 `json:"delayMS"`
	Feedback  float64 `json:"feedback"`
	DelayMix  float64 `json:"delayMix"`
	ReverbMix float64 `json:"reverbMix"`
}

type Hit struct {
	Sound      string  `json:"sound"`
	Pad        int     `json:"pad"`
	Frame      int64   `json:"frame"`
	Velocity   float64 `json:"velocity"`
	GateFrames int64   `json:"gateFrames"`
	Choke      int     `json:"choke"`
}
type Performance struct {
	Hits   []Hit `json:"hits"`
	Frames int64 `json:"frames"`
}

// Source carries asset metadata chosen by the consumer.
type Source[A any] struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Asset A         `json:"asset"`
	Peaks []float32 `json:"peaks"`
}
type Bank[A any] struct {
	Version     int         `json:"version"`
	Pads        [16]Pad     `json:"pads"`
	Effects     Effects     `json:"effects"`
	Sources     []Source[A] `json:"sources"`
	Performance Performance `json:"performance"`
}

func (b Bank[A]) Source(id string) (Source[A], bool) {
	for _, s := range b.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source[A]{}, false
}

// AudioInfo is the metadata needed to draw and trim a source in the browser.
// The host sends additional asset fields, which JSON decoding safely ignores.
type AudioInfo struct {
	Frames int64 `json:"frames"`
	RateHz int   `json:"rate_hz"`
}

func (p Pad) Validate(frames int64) error {
	if len(p.Name) > 64 || p.Start < 0 || p.End <= p.Start || p.End > frames || p.Pitch < -24 || p.Pitch > 24 ||
		!finite(p.GainDB) || p.GainDB < -60 || p.GainDB > 12 || !finite(p.AttackMS) || p.AttackMS < 0 || p.AttackMS > 500 ||
		!finite(p.ReleaseMS) || p.ReleaseMS < 2 || p.ReleaseMS > 2000 || (p.Mode != "oneshot" && p.Mode != "gate") || p.Choke < 0 || p.Choke > 4 {
		return fmt.Errorf("choose a valid source region, pitch −24 to +24, gain −60 to +12 dB, envelope, and choke group 0 to 4")
	}
	return nil
}

func (e Effects) Validate() error {
	if !finite(e.Cutoff) || e.Cutoff < 80 || e.Cutoff > 20000 || !finite(e.Drive) || e.Drive < 0 || e.Drive > 24 ||
		!finite(e.DelayMS) || e.DelayMS < 1 || e.DelayMS > 1000 || !finite(e.Feedback) || e.Feedback < 0 || e.Feedback > .7 ||
		!finite(e.DelayMix) || e.DelayMix < 0 || e.DelayMix > 1 || !finite(e.ReverbMix) || e.ReverbMix < 0 || e.ReverbMix > 1 {
		return fmt.Errorf("effect settings are outside their supported ranges")
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
