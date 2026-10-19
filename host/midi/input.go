// Package midi receives local controller input. It never opens audio devices,
// pairs Bluetooth devices, or sends messages to MIDI outputs.
package midi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
)

var ErrUnsupported = errors.New("native MIDI input is unavailable on this platform; use a browser with Web MIDI")
var ErrOverflow = errors.New("MIDI input exceeded its queue; reconnect MIDI to resume")

type Device struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Epoch uint64 `json:"epoch"`
}

// Event keeps the source's identity, UMP group, and native host timestamp.
// Data is a MIDI 1 channel message; Length distinguishes two-byte messages.
// Devices events include removed sources so a reader can release their notes.
type Event struct {
	Type       string    `json:"type"`
	Device     string    `json:"device,omitempty"`
	Epoch      uint64    `json:"epoch,omitempty"`
	Group      uint8     `json:"group,omitempty"`
	Timestamp  uint64    `json:"timestamp,omitempty"`
	HostNanos  int64     `json:"hostNanos,omitempty"`
	TimeSource string    `json:"timeSource,omitempty"`
	Position   *Position `json:"position,omitempty"`
	Data       [3]uint8  `json:"data,omitempty"`
	Length     uint8     `json:"length,omitempty"`
	Inputs     []Device  `json:"inputs,omitempty"`
	Removed    []string  `json:"removed,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// Position is the native transport position at the event's source time. Its
// epoch changes when playback restarts or moves; stale events cannot enter a
// new recording. Invalid positions retain their source time for later review.
type Position struct {
	Epoch   uint64 `json:"epoch"`
	Tick    int64  `json:"tick"`
	Playing bool   `json:"playing"`
	Valid   bool   `json:"valid"`
	Stale   bool   `json:"stale,omitempty"`
}

func scaleHostTime(stamp uint64, numerator, denominator uint32) (int64, bool) {
	if numerator == 0 || denominator == 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(stamp, uint64(numerator))
	if hi >= uint64(denominator) {
		return 0, false
	}
	nanos, _ := bits.Div64(hi, lo, uint64(denominator))
	if nanos > math.MaxInt64 {
		return 0, false
	}
	return int64(nanos), true
}

type Stream interface {
	Events() <-chan Event
	Errors() <-chan error
	Close() error
}

// InputState rejects callbacks queued by a disconnected instance, including
// after a controller reconnects with the same persistent device ID. Readers
// apply device events before delivering messages to their musical controls.
type InputState struct{ epochs map[string]uint64 }

func (s *InputState) Accept(event Event) bool {
	switch event.Type {
	case "devices":
		s.epochs = make(map[string]uint64, len(event.Inputs))
		for _, device := range event.Inputs {
			s.epochs[device.ID] = device.Epoch
		}
	case "message", "reset":
		epoch, ok := s.epochs[event.Device]
		return ok && epoch == event.Epoch
	}
	return true
}

func Open(ctx context.Context) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return openNative(ctx)
}

// CoreMIDI converts sources to the requested MIDI 1 protocol. Walk complete
// UMP messages, including unsupported payloads, without interpreting payload
// words as independent channel messages. SysEx and MIDI 2 expression stay out
// of the MIDI 1 control path.
func decodeUMP(words []uint32, emit func(Event)) error {
	for len(words) != 0 {
		word := words[0]
		var size int
		switch word >> 28 {
		case 0, 1, 2:
			size = 1
		case 3, 4:
			size = 2
		case 5, 13, 15:
			size = 4
		default:
			return fmt.Errorf("unsupported MIDI packet type %x", word>>28)
		}
		if len(words) < size {
			return fmt.Errorf("incomplete MIDI packet")
		}
		group, status := uint8(word>>24)&15, uint8(word>>16)
		switch word >> 28 {
		case 1:
			if status == 255 {
				emit(Event{Type: "reset", Group: group})
			}
		case 2:
			a, b := uint8(word>>8), uint8(word)
			if status < 128 || status >= 240 || a > 127 || b > 127 {
				return fmt.Errorf("invalid MIDI channel message")
			}
			length := uint8(3)
			if status&240 == 192 || status&240 == 208 {
				length = 2
			}
			emit(Event{Type: "message", Group: group, Data: [3]uint8{status, a, b}, Length: length})
		}
		words = words[size:]
	}
	return nil
}
