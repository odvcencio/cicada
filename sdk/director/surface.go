// Package director translates game events into the Cicada command ABI.
// Hosts enqueue commands and drain messages on the audio render thread.
package director

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/cmd"
)

// Surface is a versioned, JSON-serializable control manifest shared with JS.
type Surface struct {
	Version     int           `json:"version"`
	SampleRate  int           `json:"sample_rate"`
	Tracks      uint8         `json:"tracks"`
	Land        string        `json:"land"`
	PhraseBars  int           `json:"phrase_bars"`
	Macros      []Macro       `json:"macros"`
	States      []State       `json:"states"`
	Stingers    []Stinger     `json:"stingers"`
	Transitions []Transition  `json:"transitions"`
	Setup       []cmd.Command `json:"setup"`
}
type Macro struct {
	Name         string `json:"name"`
	ID           uint16 `json:"id"`
	SmoothFrames uint32 `json:"smooth_frames"`
}
type State struct {
	Name  string `json:"name"`
	ID    uint16 `json:"id"`
	Scene uint16 `json:"scene"`
}
type Stinger struct {
	Name            string `json:"name"`
	Track           uint8  `json:"track"`
	Slot            uint16 `json:"slot"`
	Quantize        string `json:"quantize"`
	CrossfadeFrames uint32 `json:"crossfade_frames"`
}
type Transition struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Quantize        string `json:"quantize"`
	CrossfadeFrames uint32 `json:"crossfade_frames"`
}

// Quantize resolves score landing names. Phrase uses the kernel's declared length.
func Quantize(name string, phraseBars int) (uint32, error) {
	switch name {
	case "", "now":
		return 0, nil
	case "beat":
		return 1, nil
	case "bar":
		return 2, nil
	case "2bars":
		return 6, nil
	case "4bars":
		return 8, nil
	case "phrase":
		if phraseBars >= 1 && phraseBars <= 64 {
			return uint32(cmd.QuantizePhrase), nil
		}
	}
	return 0, fmt.Errorf("unsupported director quantize %q", name)
}

// Validate rejects invalid manifests before any command is sent.
func (s Surface) Validate() error {
	if s.Version != 1 || s.Tracks < 1 || s.Tracks > 16 || (s.SampleRate != 44100 && s.SampleRate != 48000 && s.SampleRate != 96000) || s.PhraseBars < 0 || s.PhraseBars > 64 {
		return fmt.Errorf("invalid director surface")
	}
	if _, err := Quantize(s.Land, s.PhraseBars); err != nil {
		return err
	}
	if len(s.Macros) > 16 || len(s.States) > 64 || len(s.Stingers) > 64 || len(s.Transitions) > 256 || len(s.Setup) > 512 {
		return fmt.Errorf("director surface exceeds limits")
	}
	names := map[string]bool{}
	ids := map[uint16]bool{}
	unique := func(name string, id uint16, limit uint16) bool {
		if len(name) < 1 || len(name) > 64 || names[name] || id >= limit || ids[id] {
			return false
		}
		names[name], ids[id] = true, true
		return true
	}
	for _, v := range s.Macros {
		if !unique(v.Name, v.ID, 16) {
			return fmt.Errorf("invalid macro mapping")
		}
	}
	names, ids = map[string]bool{}, map[uint16]bool{}
	for _, v := range s.States {
		if !unique(v.Name, v.ID, 64) {
			return fmt.Errorf("invalid state mapping")
		}
	}
	stateNames := names
	names = map[string]bool{}
	for _, v := range s.Stingers {
		if len(v.Name) < 1 || len(v.Name) > 64 || names[v.Name] || v.Track >= s.Tracks || v.Slot >= 16 {
			return fmt.Errorf("invalid stinger mapping")
		}
		names[v.Name] = true
		if _, err := Quantize(v.Quantize, s.PhraseBars); err != nil {
			return err
		}
	}
	pairs := map[string]bool{}
	for _, v := range s.Transitions {
		key := v.From + "/" + v.To
		if !stateNames[v.From] || !stateNames[v.To] || pairs[key] {
			return fmt.Errorf("invalid transition mapping")
		}
		pairs[key] = true
		if _, err := Quantize(v.Quantize, s.PhraseBars); err != nil {
			return err
		}
	}
	for _, c := range s.Setup {
		if err := c.Validate(s.Tracks); err != nil {
			return err
		}
	}
	return nil
}

// Frames rounds milliseconds once on the host so every client gets the same ABI value.
func Frames(ms float64, rate int) uint32 {
	if ms <= 0 || rate <= 0 || math.IsNaN(ms) {
		return 0
	}
	return uint32(min(math.Round(ms*float64(rate)/1000), float64(math.MaxUint32)))
}
