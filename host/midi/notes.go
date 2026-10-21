package midi

import (
	"fmt"
	"math"
	"strings"
)

// NoteMessage is a MIDI 1 note or its complete controller snapshot. It keeps
// device/channel provenance and a unique ID across all connected inputs.
type NoteMessage struct {
	Type                         string
	Device                       string
	Channel, Note, Velocity      uint8
	ID                           uint16
	MPE                          bool
	PitchCents, Pressure, Timbre float64
	// Local and Shared retain the independent MPE layers. The fields above
	// remain the combined snapshot used by ordinary live note monitoring.
	Local      *NoteControls
	Shared     *SharedControls
	SharedOnly bool
}

type NoteControls struct{ PitchCents, Pressure, Timbre float64 }

// SharedControls identifies one device's configured zone for this input
// session. Reconnects and zone changes receive fresh identities.
type SharedControls struct {
	ID      uint64
	Device  string
	Channel uint8
	NoteControls
}

type noteChannel struct {
	bend                             uint16
	pressure, timbre, rangeSemitones float64
	rpnMSB, rpnLSB, coarse, fine     uint8
	sustain                          bool
}
type noteDevice struct {
	channels     [16]noteChannel
	lower, upper uint8
	groups       [2]uint64
}
type inputNote struct {
	NoteMessage
	released     bool
	polyPressure *float64
	frozen       *NoteMessage
}

// NoteInput runs on a host input thread, not the audio callback. Auto follows
// RPN 6 configuration; ordinary channels retain the MIDI 1 +/-2 semitone
// range. Lower/upper modes provide a starting zone for controllers which do
// not send configuration. It never guesses MPE from an ordinary pitch wheel.
type NoteInput struct {
	Mode      string // "auto" (default), "lower", or "upper"
	devices   map[string]*noteDevice
	notes     []inputNote
	nextID    uint16
	nextGroup uint64
	shared    []SharedControls
}

func (m *NoteInput) groupID() uint64 {
	m.nextGroup++
	return m.nextGroup
}

func (m *NoteInput) sharedSnapshot(device string, d *noteDevice, channel uint8) *SharedControls {
	index := 0
	if channel == 15 {
		index = 1
	} else if channel != 0 {
		return nil
	}
	if d.groups[index] == 0 {
		return nil
	}
	c := d.channels[channel]
	return &SharedControls{ID: d.groups[index], Device: device, Channel: channel,
		NoteControls: NoteControls{bendValue(c), c.pressure, c.timbre}}
}

func (m *NoteInput) publishShared(device string, d *noteDevice, channel uint8) {
	if value := m.sharedSnapshot(device, d, channel); value != nil {
		m.shared = append(m.shared, *value)
	}
}

// DisconnectPrefix also clears controller state for native MIDI groups.
func (m *NoteInput) DisconnectPrefix(prefix string) []NoteMessage {
	var result []NoteMessage
	for device := range m.devices {
		if strings.HasPrefix(device, prefix) {
			result = append(result, m.Disconnect(device)...)
		}
	}
	return result
}

func defaultNoteChannel() noteChannel {
	return noteChannel{bend: 8192, timbre: .5, rangeSemitones: 2, coarse: 2, rpnMSB: 127, rpnLSB: 127}
}
func (d *noteDevice) master(channel uint8) (uint8, bool) {
	if d.lower > 0 && channel > 0 && channel <= d.lower {
		return 0, true
	}
	if d.upper > 0 && channel < 15 && channel >= 15-d.upper {
		return 15, true
	}
	return 0, false
}
func bendValue(c noteChannel) float64 {
	scale := float64(8191)
	if c.bend < 8192 {
		scale = 8192
	}
	return float64(int(c.bend)-8192) / scale * c.rangeSemitones * 100
}
func (m *NoteInput) snapshot(n inputNote) NoteMessage {
	d := m.devices[n.Device]
	c := d.channels[n.Channel]
	value := n.NoteMessage
	value.Type = "note-expression"
	value.PitchCents, value.Pressure, value.Timbre = bendValue(c), c.pressure, c.timbre
	if n.polyPressure != nil {
		value.Pressure = *n.polyPressure
	}
	if n.frozen != nil {
		value.PitchCents, value.Pressure, value.Timbre = n.frozen.PitchCents, n.frozen.Pressure, n.frozen.Timbre
	}
	if master, ok := d.master(n.Channel); ok && n.MPE {
		value.Local = &NoteControls{value.PitchCents, value.Pressure, value.Timbre}
		value.Shared = m.sharedSnapshot(n.Device, d, master)
		zone := d.channels[master]
		value.PitchCents += bendValue(zone)
		value.Pressure = math.Max(value.Pressure, zone.pressure)
		value.Timbre = max(0, min(1, value.Timbre+zone.timbre-.5))
	}
	value.PitchCents = max(-9600, min(9600, value.PitchCents))
	return value
}
func (m *NoteInput) device(id string) (*noteDevice, error) {
	if id == "" || len(id) > 512 {
		return nil, fmt.Errorf("invalid MIDI device identity")
	}
	if d := m.devices[id]; d != nil {
		return d, nil
	}
	if len(m.devices) >= 64 {
		return nil, fmt.Errorf("MIDI input device limit reached")
	}
	if m.devices == nil {
		m.devices = make(map[string]*noteDevice)
	}
	d := new(noteDevice)
	for i := range d.channels {
		d.channels[i] = defaultNoteChannel()
	}
	if m.Mode == "lower" {
		d.lower = 15
		d.groups[0] = m.groupID()
	} else if m.Mode == "upper" {
		d.upper = 15
		d.groups[1] = m.groupID()
	}
	for i := range d.channels {
		if _, ok := d.master(uint8(i)); ok {
			d.channels[i].rangeSemitones = 48
			d.channels[i].coarse = 48
		}
	}
	m.devices[id] = d
	return d, nil
}
func (m *NoteInput) allocateID() uint16 {
	for {
		m.nextID++
		if m.nextID == 0 || m.nextID == 65535 {
			m.nextID = 1
		}
		used := false
		for _, n := range m.notes {
			if n.ID == m.nextID {
				used = true
				break
			}
		}
		if !used {
			return m.nextID
		}
	}
}
func (m *NoteInput) releaseMatching(match func(inputNote) bool) []NoteMessage {
	var result []NoteMessage
	kept := m.notes[:0]
	for _, n := range m.notes {
		if match(n) {
			value := n.NoteMessage
			value.Type = "note-off"
			result = append(result, value)
		} else {
			kept = append(kept, n)
		}
	}
	clear(m.notes[len(kept):])
	m.notes = kept
	return result
}
func (m *NoteInput) Disconnect(device string) []NoteMessage {
	result := m.releaseMatching(func(n inputNote) bool { return n.Device == device })
	delete(m.devices, device)
	return result
}
func (m *NoteInput) Reset(mode string) []NoteMessage {
	result := m.releaseMatching(func(inputNote) bool { return true })
	m.devices = nil
	m.Mode = mode
	return result
}

func (m *NoteInput) freeze(n *inputNote, c noteChannel) {
	if !n.MPE {
		return
	}
	value := n.NoteMessage
	value.PitchCents, value.Pressure, value.Timbre = bendValue(c), c.pressure, c.timbre
	if n.polyPressure != nil {
		value.Pressure = *n.polyPressure
	}
	n.frozen = &value
}

// Process accepts complete channel messages. SysEx, realtime, program changes
// and unrelated CCs do not create notes. Releases pair retriggers in arrival
// order. Sustain and zone reconfiguration retain no orphaned identities.
func (m *NoteInput) Process(device string, data []byte) ([]NoteMessage, error) {
	m.shared = nil
	if len(data) < 1 || data[0] < 0x80 || data[0] >= 0xf0 {
		return nil, nil
	}
	status, channel := data[0]&0xf0, data[0]&15
	size := 3
	if status == 0xc0 || status == 0xd0 {
		size = 2
	}
	if len(data) < size || data[1] > 127 || size == 3 && data[2] > 127 {
		return nil, fmt.Errorf("invalid MIDI channel message")
	}
	if status == 0xc0 {
		return nil, nil
	}
	d, err := m.device(device)
	if err != nil {
		return nil, err
	}
	a, b := data[1], byte(0)
	if size == 3 {
		b = data[2]
	}
	c := &d.channels[channel]
	if status == 0x90 && b > 0 {
		if len(m.notes) >= 128 {
			return nil, fmt.Errorf("MIDI input needs a release before more than 128 held notes")
		}
		_, mpe := d.master(channel)
		n := inputNote{NoteMessage: NoteMessage{Type: "note-on", Device: device, Channel: channel, Note: a, Velocity: b, ID: m.allocateID(), MPE: mpe}}
		m.notes = append(m.notes, n)
		value := m.snapshot(n)
		value.Type = "note-on"
		return []NoteMessage{value}, nil
	}
	if status == 0x80 || status == 0x90 {
		for i := range m.notes {
			n := &m.notes[i]
			if n.Device != device || n.Channel != channel || n.Note != a || n.released {
				continue
			}
			n.released = true
			pedal := c.sustain
			if master, ok := d.master(channel); ok {
				pedal = pedal || d.channels[master].sustain
			}
			if pedal {
				m.freeze(n, *c)
				return nil, nil
			}
			id := n.ID
			return m.releaseMatching(func(n inputNote) bool { return n.ID == id }), nil
		}
		return nil, nil
	}
	zoneAffects := func(n inputNote) bool {
		if n.Device != device {
			return false
		}
		master, member := d.master(n.Channel)
		return n.Channel == channel || member && master == channel
	}
	if status == 0xb0 {
		switch a {
		case 64:
			c.sustain = b >= 64
			if c.sustain {
				return nil, nil
			}
			return m.releaseMatching(func(n inputNote) bool {
				if !zoneAffects(n) || !n.released {
					return false
				}
				pedal := d.channels[n.Channel].sustain
				if master, ok := d.master(n.Channel); ok {
					pedal = pedal || d.channels[master].sustain
				}
				return !pedal
			}), nil
		case 120:
			c.sustain = false
			return m.releaseMatching(zoneAffects), nil
		case 123:
			for i := range m.notes {
				n := &m.notes[i]
				if zoneAffects(*n) {
					n.released = true
					m.freeze(n, d.channels[n.Channel])
				}
			}
			return m.releaseMatching(func(n inputNote) bool {
				if !zoneAffects(n) {
					return false
				}
				sustain := d.channels[n.Channel].sustain
				if master, ok := d.master(n.Channel); ok {
					sustain = sustain || d.channels[master].sustain
				}
				return !sustain
			}), nil
		case 101:
			c.rpnMSB = b
			return nil, nil
		case 100:
			c.rpnLSB = b
			return nil, nil
		case 6, 38:
			if c.rpnMSB == 0 && c.rpnLSB == 6 && a == 6 && (channel == 0 || channel == 15) && b <= 15 {
				old := *d
				if channel == 0 {
					d.lower = b
					if b > 0 && d.upper > 0 && int(b)+int(d.upper) > 14 {
						d.upper = uint8(max(0, 14-int(b)))
					}
				} else {
					d.upper = b
					if b > 0 && d.lower > 0 && int(b)+int(d.lower) > 14 {
						d.lower = uint8(max(0, 14-int(b)))
					}
				}
				for index, count := range []uint8{d.lower, d.upper} {
					if count == 0 {
						d.groups[index] = 0
					} else if d.groups[index] == 0 || index == 0 && channel == 0 || index == 1 && channel == 15 {
						d.groups[index] = m.groupID()
					}
				}
				affected := [16]bool{}
				for i := range d.channels {
					om, okOld := old.master(uint8(i))
					nm, okNew := d.master(uint8(i))
					affected[i] = okOld != okNew || om != nm || channel == uint8(i) || okNew && nm == channel
					if affected[i] {
						d.channels[i] = defaultNoteChannel()
						if okNew {
							d.channels[i].rangeSemitones, d.channels[i].coarse = 48, 48
						}
					}
				}
				return m.releaseMatching(func(n inputNote) bool { return n.Device == device && affected[n.Channel] }), nil
			}
			if c.rpnMSB != 0 || c.rpnLSB != 0 {
				return nil, nil
			}
			if a == 6 {
				c.coarse = b
			} else {
				c.fine = min(b, 99)
			}
			c.rangeSemitones = min(96, float64(c.coarse)+float64(c.fine)/100)
		case 74:
			c.timbre = float64(b) / 127
		case 121:
			for i := range d.channels {
				dummy := inputNote{NoteMessage: NoteMessage{Device: device, Channel: uint8(i)}}
				if zoneAffects(dummy) {
					previous := d.channels[i]
					d.channels[i] = defaultNoteChannel()
					d.channels[i].rangeSemitones, d.channels[i].coarse, d.channels[i].fine = previous.rangeSemitones, previous.coarse, previous.fine
				}
			}
			m.publishShared(device, d, channel)
			releases := m.releaseMatching(func(n inputNote) bool { return zoneAffects(n) && n.released })
			for i := range m.notes {
				if zoneAffects(m.notes[i]) {
					m.notes[i].polyPressure = nil
					m.notes[i].frozen = nil
					releases = append(releases, m.snapshot(m.notes[i]))
				}
			}
			return releases, nil
		default:
			return nil, nil
		}
	} else if status == 0xe0 {
		c.bend = uint16(a) | uint16(b)<<7
	} else if status == 0xd0 {
		c.pressure = float64(a) / 127
	} else if status != 0xa0 {
		return nil, nil
	}
	if status != 0xa0 {
		m.publishShared(device, d, channel)
	}
	var result []NoteMessage
	for i := range m.notes {
		n := &m.notes[i]
		if !zoneAffects(*n) {
			continue
		}
		if status == 0xa0 {
			if _, member := d.master(channel); member || n.Note != a {
				continue
			}
			pressure := float64(b) / 127
			n.polyPressure = &pressure
		} else if status == 0xd0 && n.Channel == channel {
			n.polyPressure = nil
		}
		if n.released && n.MPE && n.Channel == channel {
			continue
		}
		value := m.snapshot(*n)
		value.SharedOnly = n.MPE && n.Channel != channel
		result = append(result, value)
	}
	return result, nil
}

// ProcessEvents also returns shared master controls when every note is already
// released. Capture can retain release-tail movement without extending gates.
func (m *NoteInput) ProcessEvents(device string, data []byte) ([]NoteMessage, []SharedControls, error) {
	notes, err := m.Process(device, data)
	return notes, m.shared, err
}
