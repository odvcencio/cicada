package notation

import (
	"fmt"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/internal/paramdefs"
)

func parseMIDI(w *loweringWalker, node *gts.Node) *MIDI {
	midi := &MIDI{Position: w.position(node)}
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		kind := w.Type(child)
		if kind == "midi_port" {
			midi.Ports = append(midi.Ports, MIDIPort{Name: w.Text(w.Field(child, "name")), Position: w.position(child)})
			continue
		}
		if kind != "midi_track" && kind != "midi_cc" && kind != "midi_note" {
			continue
		}
		m := MIDIMapping{Port: w.Text(w.Field(child, "port")), Message: strings.TrimPrefix(kind, "midi_"), Position: w.position(child)}
		if channel := w.Text(w.Field(child, "channel")); channel != "" {
			var err error
			m.Channel, err = strconv.Atoi(channel)
			if err != nil || m.Channel == 0 {
				m.Channel = -1
			}
		}
		m.Target = w.Text(w.Field(child, "target"))
		m.Min, m.Max = w.Text(w.Field(child, "min")), w.Text(w.Field(child, "max"))
		m.Takeover, m.Mode = w.Text(w.Field(child, "takeover")), w.Text(w.Field(child, "mode"))
		if kind == "midi_cc" {
			var err error
			m.Number, err = strconv.Atoi(w.Text(w.Field(child, "number")))
			if err != nil {
				m.Number = -1
			}
		} else if kind == "midi_note" {
			m.Number = midiPitch(w.Text(w.Field(child, "number")))
			m.Target = w.Text(w.Field(child, "action"))
			if scene := w.Text(w.Field(child, "scene")); scene != "" {
				m.Target = "launch " + scene
			}
		}
		midi.Mappings = append(midi.Mappings, m)
	}
	return midi
}

func midiPitch(text string) int {
	if n, err := strconv.Atoi(text); err == nil {
		return n
	}
	if len(text) < 2 || len(text) > 3 || text[len(text)-1] < '0' || text[len(text)-1] > '6' {
		return -1
	}
	semitone, ok := map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}[text[0]]
	if !ok {
		return -1
	}
	if len(text) == 3 {
		switch text[1] {
		case '#':
			semitone++
		case 'b':
			semitone--
		default:
			return -1
		}
	}
	return 12*(int(text[len(text)-1]-'0')+1) + semitone
}

func validateMIDI(s *Score) []Diagnostic {
	if s.MIDI == nil {
		return nil
	}
	var ds []Diagnostic
	add := func(code, message string, p Position) {
		ds = append(ds, Diagnostic{Code: code, Message: message, Severity: "error", Position: p})
	}
	if s.Version != 2 {
		add("CICADA-VERSION", "MIDI mappings require edition 2", s.MIDI.Position)
	}
	ports, seen := map[string]bool{}, map[string]bool{}
	for _, p := range s.MIDI.Ports {
		if ports[p.Name] {
			add("CICADA-MIDI", "duplicate MIDI port "+p.Name, p.Position)
		}
		ports[p.Name] = true
	}
	if len(ports) > 32 || len(s.MIDI.Mappings) > 256 {
		add("CICADA-LIMIT", "MIDI supports at most 32 ports and 256 mappings", s.MIDI.Position)
	}
	for _, m := range s.MIDI.Mappings {
		if !ports[m.Port] {
			add("CICADA-MIDI", "unknown MIDI port "+m.Port, m.Position)
		}
		if m.Channel < 0 || m.Channel > 16 || m.Message == "track" && m.Channel == 0 {
			add("CICADA-MIDI", "MIDI channel must be 1 to 16", m.Position)
		}
		if m.Message != "track" && (m.Number < 0 || m.Number > 127) {
			add("CICADA-MIDI", "MIDI note or controller must be 0 to 127", m.Position)
		}
		key := fmt.Sprintf("%s/%s/%d/%d", m.Port, m.Message, m.Channel, m.Number)
		if seen[key] {
			add("CICADA-MIDI", "duplicate MIDI input mapping", m.Position)
		}
		seen[key] = true
		switch m.Message {
		case "track":
			found := false
			for _, t := range s.Tracks {
				found = found || t.Name == m.Target
			}
			if !found {
				add("CICADA-MIDI", "unknown MIDI track "+m.Target, m.Position)
			}
		case "cc":
			desc, _, _, code, msg := resolveNotationPath(s, m.Target)
			if code != "" {
				add(code, msg, m.Position)
				break
			}
			if !desc.Live {
				add("CICADA-UNSUPPORTED", "MIDI target is not live: "+m.Target, m.Position)
			}
			if m.Takeover != "" && m.Takeover != "jump" {
				add("CICADA-UNSUPPORTED", "MIDI takeover "+m.Takeover+" is not implemented", m.Position)
			}
			for _, value := range []string{m.Min, m.Max} {
				if value != "" {
					if err := validateSceneValue(desc, m.Target, value); err != nil {
						add("CICADA-MIDI", "invalid MIDI range: "+err.Error(), m.Position)
					}
				}
			}
			if m.Min != "" && m.Max != "" {
				min, _, minErr := paramdefs.ParseBase(m.Min)
				max, _, maxErr := paramdefs.ParseBase(m.Max)
				if minErr == nil && maxErr == nil && min >= max {
					add("CICADA-MIDI", "MIDI range minimum must be below maximum", m.Position)
				}
			}
		case "note":
			if m.Mode != "" && m.Mode != "trigger" {
				add("CICADA-UNSUPPORTED", "MIDI launch mode "+m.Mode+" is not implemented", m.Position)
			}
			if m.Target != "play" && m.Target != "stop" {
				name := strings.TrimSpace(strings.TrimPrefix(m.Target, "launch"))
				found := false
				for _, scene := range s.Scenes {
					found = found || scene.Name == name
				}
				if !strings.HasPrefix(m.Target, "launch") || !found {
					add("CICADA-MIDI", "unknown MIDI action "+m.Target, m.Position)
				}
			}
		}
	}
	return ds
}
