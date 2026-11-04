package project

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// Midi contains the score's portable logical ports; machine device names stay
// in cicada.local and never enter semantic interchange or bundles.
type Midi struct {
	Ports []string      `cicada:"Logical input port names" json:"ports" introduced:"cicada.project/2"`
	Maps  []MidiMapping `cicada:"Input mappings" json:"maps" introduced:"cicada.project/2"`
}

type MidiMapping struct {
	Port     string     `cicada:"Logical input port" json:"port" introduced:"cicada.project/2"`
	Message  string     `cicada:"Input message kind" json:"message" introduced:"cicada.project/2"`
	Channel  int        `cicada:"MIDI channel or zero for any" json:"channel,omitempty" introduced:"cicada.project/2"`
	Number   *int       `cicada:"Controller or note number" json:"number,omitempty" introduced:"cicada.project/2"`
	Target   string     `cicada:"Track, parameter or action" json:"target" introduced:"cicada.project/2"`
	Range    *MidiRange `cicada:"Controller range in parameter units" json:"range,omitempty" introduced:"cicada.project/2"`
	Takeover string     `cicada:"Controller takeover mode" json:"takeover,omitempty" introduced:"cicada.project/2"`
	Mode     string     `cicada:"Launch behavior" json:"mode,omitempty" introduced:"cicada.project/2"`
}

type MidiRange struct {
	Min Value `cicada:"Minimum parameter value" json:"min" introduced:"cicada.project/2"`
	Max Value `cicada:"Maximum parameter value" json:"max" introduced:"cicada.project/2"`
}

func midiFromScore(source *notation.MIDI) *Midi {
	if source == nil {
		return nil
	}
	m := &Midi{Ports: []string{}, Maps: []MidiMapping{}}
	for _, p := range source.Ports {
		m.Ports = append(m.Ports, p.Name)
	}
	for _, item := range source.Mappings {
		v := MidiMapping{Port: item.Port, Message: item.Message, Channel: item.Channel, Target: item.Target, Takeover: item.Takeover, Mode: item.Mode}
		if item.Message != "track" {
			number := item.Number
			v.Number = &number
		}
		if item.Min != "" && item.Max != "" {
			min, _ := projectValue(item.Min)
			max, _ := projectValue(item.Max)
			v.Range = &MidiRange{Min: min, Max: max}
		}
		m.Maps = append(m.Maps, v)
	}
	return m
}

func midiSource(m *Midi) (string, error) {
	var out strings.Builder
	out.WriteString("midi {\n")
	for _, port := range m.Ports {
		out.WriteString("  port " + port + "\n")
	}
	for _, item := range m.Maps {
		out.WriteString("  " + item.Port)
		if item.Channel != 0 {
			out.WriteString(" ch " + strconv.Itoa(item.Channel))
		}
		switch item.Message {
		case "cc", "note":
			if item.Number == nil {
				return "", fmt.Errorf("CICADA-MIDI: missing input number")
			}
			out.WriteString(" " + item.Message + " " + strconv.Itoa(*item.Number))
		case "track":
		default:
			return "", fmt.Errorf("CICADA-MIDI: unknown message %s", item.Message)
		}
		out.WriteString(" -> " + item.Target)
		if item.Range != nil {
			min, err := valueSource(item.Range.Min)
			if err != nil {
				return "", err
			}
			max, err := valueSource(item.Range.Max)
			if err != nil {
				return "", err
			}
			out.WriteString(" " + min + ".." + max)
		}
		if item.Takeover != "" {
			out.WriteString(" takeover = " + item.Takeover)
		}
		if item.Mode != "" {
			out.WriteString(" mode = " + item.Mode)
		}
		out.WriteByte('\n')
	}
	out.WriteByte('}')
	return out.String(), nil
}

func validateMidiProject(p *Project) error {
	if p.MIDI == nil {
		return nil
	}
	if p.Edition != 2 || p.Format != FormatID2 {
		return fmt.Errorf("CICADA-VERSION: MIDI mappings require edition 2 and cicada.project/2")
	}
	if p.MIDI.Ports == nil || p.MIDI.Maps == nil || len(p.MIDI.Ports) > 32 || len(p.MIDI.Maps) > 256 {
		return fmt.Errorf("CICADA-MIDI: invalid MIDI mapping arrays")
	}
	ports, seen := map[string]bool{}, map[string]bool{}
	for _, port := range p.MIDI.Ports {
		if !validID(port) || ports[port] {
			return fmt.Errorf("CICADA-MIDI: duplicate or invalid port %s", port)
		}
		ports[port] = true
	}
	for _, item := range p.MIDI.Maps {
		if !ports[item.Port] || item.Channel < 0 || item.Channel > 16 {
			return fmt.Errorf("CICADA-MIDI: unknown port or invalid channel")
		}
		if item.Message == "track" {
			if item.Channel == 0 || item.Number != nil || item.Range != nil || item.Takeover != "" || item.Mode != "" {
				return fmt.Errorf("CICADA-MIDI: invalid track mapping")
			}
			found := false
			for _, track := range p.Tracks {
				found = found || track.ID == item.Target
			}
			if !found {
				return fmt.Errorf("CICADA-MIDI: unknown track %s", item.Target)
			}
		} else if item.Message == "cc" || item.Message == "note" {
			if item.Number == nil || *item.Number < 0 || *item.Number > 127 {
				return fmt.Errorf("CICADA-MIDI: invalid input number")
			}
			if item.Message == "cc" {
				resolved, err := ResolveParameterPath(p, item.Target)
				if err != nil {
					return err
				}
				if !resolved.Descriptor.Live {
					return fmt.Errorf("CICADA-UNSUPPORTED: MIDI target is not live")
				}
				if item.Mode != "" || item.Takeover != "" && item.Takeover != "jump" && item.Takeover != "pickup" {
					return fmt.Errorf("CICADA-UNSUPPORTED: MIDI takeover mode")
				}
				if item.Range != nil {
					min, err := valueSource(item.Range.Min)
					if err != nil {
						return err
					}
					max, err := valueSource(item.Range.Max)
					if err != nil {
						return err
					}
					if _, err := paramdefs.ValidateLiteral(resolved.Descriptor, min); err != nil {
						return err
					}
					if _, err := paramdefs.ValidateLiteral(resolved.Descriptor, max); err != nil {
						return err
					}
					if item.Range.Min.Number == nil || item.Range.Max.Number == nil || *item.Range.Min.Number >= *item.Range.Max.Number {
						return fmt.Errorf("CICADA-MIDI: invalid controller range")
					}
				}
			} else {
				if item.Range != nil || item.Takeover != "" || item.Mode != "" && item.Mode != "trigger" {
					return fmt.Errorf("CICADA-UNSUPPORTED: MIDI launch mode")
				}
				if item.Target != "play" && item.Target != "stop" {
					name := strings.TrimSpace(strings.TrimPrefix(item.Target, "launch "))
					found := false
					for _, scene := range p.Scenes {
						found = found || scene.ID == name
					}
					if !strings.HasPrefix(item.Target, "launch ") || !found {
						return fmt.Errorf("CICADA-MIDI: unknown action %s", item.Target)
					}
				}
			}
		} else {
			return fmt.Errorf("CICADA-MIDI: invalid message kind")
		}
		number := -1
		if item.Number != nil {
			number = *item.Number
		}
		key := fmt.Sprintf("%s/%s/%d/%d", item.Port, item.Message, item.Channel, number)
		if seen[key] {
			return fmt.Errorf("CICADA-MIDI: duplicate input mapping")
		}
		seen[key] = true
	}
	return nil
}
