package control

// PortableMIDI is the score's logical MIDI projection. Physical device names
// come from a separate, machine-local port map.
type PortableMIDI struct {
	Ports []string          `json:"ports"`
	Maps  []PortableMapping `json:"maps"`
}

type PortableMapping struct {
	Port    string         `json:"port"`
	Message string         `json:"message"`
	Channel int            `json:"channel"` // zero means any channel
	Number  *int           `json:"number"`
	Target  string         `json:"target"`
	Range   *PortableRange `json:"range"`
}

type PortableRange struct {
	Min PortableValue `json:"min"`
	Max PortableValue `json:"max"`
}

type PortableValue struct {
	Number *float64 `json:"number"`
}

// Match chooses a channel-specific mapping before an any-channel mapping.
// If multiple logical ports resolve to one device, declaration order wins.
func (m PortableMIDI) Match(ports map[string]string, device string, channel int, message string, number int) (PortableMapping, bool) {
	for _, specific := range []bool{true, false} {
		for _, mapping := range m.Maps {
			if ports[mapping.Port] == "" || ports[mapping.Port] != device || mapping.Message != message {
				continue
			}
			if specific && mapping.Channel != channel || !specific && mapping.Channel != 0 {
				continue
			}
			if message != "track" && (mapping.Number == nil || *mapping.Number != number) {
				continue
			}
			return mapping, true
		}
	}
	return PortableMapping{}, false
}
