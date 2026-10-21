// Package control holds deterministic, browser-independent MIDI and take logic.
package control

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/host/midi"
)

type Descriptor struct {
	Min    float64  `json:"min"`
	Max    float64  `json:"max"`
	Curve  string   `json:"curve"`
	Off    bool     `json:"off"`
	Values []string `json:"values"`
}

// CCValue maps MIDI controller values using the domain registry's curves.
// A nil value is the registry's explicit off state, not a numeric infinity.
func CCValue(d Descriptor, cc int) (*float64, error) {
	if cc < 0 || cc > 127 || math.IsNaN(d.Min) || math.IsNaN(d.Max) || math.IsInf(d.Min, 0) || math.IsInf(d.Max, 0) || d.Max < d.Min {
		return nil, fmt.Errorf("invalid controller value or parameter range")
	}
	u := float64(cc) / 127
	v := d.Min + u*(d.Max-d.Min)
	switch d.Curve {
	case "log":
		if d.Min > 0 && d.Max > 0 {
			v = math.Exp(math.Log(d.Min) + u*(math.Log(d.Max)-math.Log(d.Min)))
		}
	case "fader":
		if d.Off && cc == 0 {
			return nil, nil
		}
		if cc == 0 {
			v = d.Min
		} else {
			v = math.Max(d.Min, math.Min(d.Max, 20*math.Log10(u*math.Pow(10, d.Max/20))))
		}
	case "toggle":
		v = d.Min
		if cc >= 64 {
			v = d.Max
		}
	case "enum":
		n := len(d.Values)
		if n < 2 {
			n = int(math.Round(d.Max-d.Min)) + 1
		}
		if n < 2 {
			n = 2
		}
		v = d.Min + math.Round(u*float64(n-1))*(d.Max-d.Min)/float64(n-1)
	}
	return &v, nil
}

type Note struct {
	Tick         int64            `json:"tick"`
	EndTick      int64            `json:"endTick"`
	Note         int              `json:"note"`
	Velocity     int              `json:"velocity"`
	NoteID       uint16           `json:"noteId,omitempty"`
	Channel      uint8            `json:"channel,omitempty"`
	Group        uint16           `json:"group,omitempty"`
	Expressions  []NoteExpression `json:"expressions,omitempty"`
	Input        *midi.NoteTiming `json:"input,omitempty"`
	EndEstimated bool             `json:"endEstimated,omitempty"`
}
type Recording struct {
	Track   string          `json:"track"`
	Pattern string          `json:"pattern"`
	Notes   []Note          `json:"notes"`
	Groups  []RecordedGroup `json:"groups,omitempty"`
}

type RecordedGroup struct {
	ID      uint64           `json:"id"`
	Device  string           `json:"device"`
	Channel uint8            `json:"channel"`
	Points  []NoteExpression `json:"points"`
}

func ValidateRecordings(takes []Recording) error {
	if len(takes) == 0 || len(takes) > 32 {
		return fmt.Errorf("a take needs 1 to 32 track/pattern recordings")
	}
	count := 0
	points := 0
	for _, take := range takes {
		if take.Track == "" || take.Pattern == "" || len(take.Notes) == 0 {
			return fmt.Errorf("each recording needs a track, pattern, and notes")
		}
		count += len(take.Notes)
		if len(take.Groups) > 512 {
			return fmt.Errorf("a recording may contain at most 512 shared gesture lanes")
		}
		groups := map[uint64]bool{}
		for _, group := range take.Groups {
			if group.ID == 0 || groups[group.ID] || group.Device == "" || len(group.Device) > 512 || group.Channel != 0 && group.Channel != 15 || len(group.Points) == 0 {
				return fmt.Errorf("shared gestures need unique input identities, a master channel and points")
			}
			groups[group.ID] = true
			previous := int64(0)
			for _, point := range group.Points {
				if point.Tick < previous || point.Tick >= 1<<60 || !point.Valid() || point.VibratoDepthCents != nil {
					return fmt.Errorf("shared gestures must be ordered, finite controller snapshots")
				}
				previous = point.Tick
			}
			points += len(group.Points)
		}
		for _, n := range take.Notes {
			if n.Tick < 0 || n.EndTick < n.Tick || n.EndTick > 1<<60 || n.Note < 0 || n.Note > 127 || n.Velocity < 1 || n.Velocity > 127 || n.NoteID == 65535 || n.Channel > 15 || int(n.Group) > len(take.Groups) {
				return fmt.Errorf("recorded note range or timing is invalid")
			}
			previous := n.Tick
			for _, expression := range n.Expressions {
				if expression.Tick < previous || expression.Tick > n.EndTick || !expression.Valid() {
					return fmt.Errorf("recorded gestures must be ordered within their note, with finite bend, pressure and timbre")
				}
				previous = expression.Tick
			}
			points += len(n.Expressions)
			if n.Input != nil && (n.Input.HostNanos <= 0 || n.Input.EndHostNanos != 0 && n.Input.EndHostNanos < n.Input.HostNanos || n.Input.Source != "source" && n.Input.Source != "arrival" || n.Input.EndSource != "" && n.Input.EndSource != "source" && n.Input.EndSource != "arrival") {
				return fmt.Errorf("recorded input timing is invalid")
			}
		}
	}
	if count > 512 {
		return fmt.Errorf("a take may contain at most 512 notes")
	}
	if points > MaxExpressionPoints {
		return fmt.Errorf("a take may contain at most %d gesture points", MaxExpressionPoints)
	}
	return nil
}

func GMDrum(note int) bool {
	switch note {
	case 36, 37, 38, 39, 41, 42, 43, 45, 46, 47, 48, 49, 50, 56, 57:
		return true
	}
	return false
}
