// Package padsampler defines the portable pad bank and performance protocol.
// The browser shares these types; audio preparation and storage stay on the host.
package padsampler

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
type Source struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Asset sourceAsset `json:"asset"`
	Peaks []float32   `json:"peaks"`
}
type Bank struct {
	Version     int         `json:"version"`
	Pads        [16]Pad     `json:"pads"`
	Effects     Effects     `json:"effects"`
	Sources     []Source    `json:"sources"`
	Performance Performance `json:"performance"`
}

func (b Bank) Source(id string) (Source, bool) {
	for _, s := range b.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}
