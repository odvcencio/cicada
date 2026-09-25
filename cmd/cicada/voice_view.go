package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/project"
)

type viewSignal struct {
	Label    string
	Kind     string
	Children []viewSignal
}

type viewSignalBinding struct {
	Name string
	Tree viewSignal
}

type viewVoiceParam struct {
	Name, Default string
	Overrides     []string
}

type viewVoiceMap struct {
	Lane, Target string
}

type viewVoice struct {
	ID, Kind, Mode, Octave, Description string
	UsedBy                              []string
	Params                              []viewVoiceParam
	Lets                                []viewSignalBinding
	Output                              viewSignal
	Mappings                            []viewVoiceMap
}

func scoreVoices(p *project.Project) []viewVoice {
	voices := make([]viewVoice, 0, len(p.Instruments)+len(p.Kits)+2)
	for _, instrument := range p.Instruments {
		voice := viewVoice{ID: instrument.ID, Kind: "authored", Mode: instrument.Mode,
			Description: "Programmable signal graph", Output: viewSignalTree(instrument.Out)}
		if instrument.Octave != nil {
			voice.Octave = strconv.Itoa(*instrument.Octave)
		}
		for _, param := range instrument.Params {
			entry := viewVoiceParam{Name: param.ID, Default: formatVoiceNumber(param.Default, param.Unit)}
			for _, track := range p.Tracks {
				if track.Kind == instrument.ID {
					if value, ok := track.Params[param.ID]; ok {
						entry.Overrides = append(entry.Overrides, track.ID+": "+formatVoiceValue(value))
					}
				}
			}
			voice.Params = append(voice.Params, entry)
		}
		for _, binding := range instrument.Lets {
			voice.Lets = append(voice.Lets, viewSignalBinding{Name: binding.ID, Tree: viewSignalTree(binding.Value)})
		}
		for _, track := range p.Tracks {
			if track.Kind == instrument.ID {
				voice.UsedBy = append(voice.UsedBy, "track "+track.ID)
			}
		}
		for _, kit := range p.Kits {
			for _, lane := range drumLaneOrder {
				if kit.Lanes[lane] == instrument.ID {
					voice.UsedBy = append(voice.UsedBy, "kit "+kit.ID+" / "+strings.ToUpper(lane))
				}
			}
		}
		voices = append(voices, voice)
	}
	for _, builtin := range []string{"acid", "drums"} {
		voice := viewVoice{ID: builtin, Kind: "built-in", Mode: "native", Description: "Native Cicada voice"}
		for _, track := range p.Tracks {
			if track.Kind != builtin {
				continue
			}
			voice.UsedBy = append(voice.UsedBy, "track "+track.ID)
			names := make([]string, 0, len(track.Params))
			for name := range track.Params {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				voice.Params = append(voice.Params, viewVoiceParam{Name: track.ID + " / " + name, Default: formatVoiceValue(track.Params[name])})
			}
		}
		if len(voice.UsedBy) > 0 {
			voices = append(voices, voice)
		}
	}
	for _, kit := range p.Kits {
		voice := viewVoice{ID: kit.ID, Kind: "kit", Mode: "drums", Description: "Drum lane sound routing"}
		for _, track := range p.Tracks {
			if track.Kind == kit.ID {
				voice.UsedBy = append(voice.UsedBy, "track "+track.ID)
			}
		}
		for _, lane := range drumLaneOrder {
			if target, ok := kit.Lanes[lane]; ok {
				voice.Mappings = append(voice.Mappings, viewVoiceMap{Lane: strings.ToUpper(lane), Target: target})
			}
		}
		voices = append(voices, voice)
	}
	return voices
}

func viewSignalTree(expr project.Expr) viewSignal {
	if expr.Literal != nil {
		return viewSignal{Label: strconv.FormatFloat(*expr.Literal, 'f', -1, 64), Kind: "literal"}
	}
	if expr.Name != "" {
		return viewSignal{Label: expr.Name, Kind: "name"}
	}
	node := viewSignal{Label: expr.Op, Kind: "operator", Children: make([]viewSignal, 0, len(expr.Args))}
	for _, child := range expr.Args {
		node.Children = append(node.Children, viewSignalTree(child))
	}
	return node
}

func formatVoiceValue(value project.Value) string {
	if value.Number != nil {
		return formatVoiceNumber(*value.Number, value.Unit)
	}
	return value.Text
}

func formatVoiceNumber(value float64, unit string) string {
	label := strconv.FormatFloat(value, 'f', -1, 64)
	switch strings.ToLower(unit) {
	case "hz":
		return label + "Hz"
	case "ms":
		return label + "ms"
	case "s":
		return label + "s"
	case "db":
		return label + "dB"
	case "percent", "%":
		return label + "%"
	case "", "ratio", "enum":
		return label
	default:
		return fmt.Sprintf("%s %s", label, unit)
	}
}
