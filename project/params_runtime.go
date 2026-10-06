package project

import (
	_ "embed"
	"fmt"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
)

//go:embed params.json
var paramsRegistryJSON string

type ParamAddress struct {
	Address string `json:"address"`
	Param   string `json:"param"`
	Track   string `json:"track,omitempty"`
	Value   any    `json:"value"`
}

func ParamsJSON() string { return paramsRegistryJSON }

// ParamAddresses returns the current compiled values for every registry kind
// that exists on this score. Live-only mute and solo start off for the session.
func ParamAddresses(p *Project) []ParamAddress {
	if p == nil {
		return []ParamAddress{}
	}
	addresses := make([]ParamAddress, 0, len(p.Tracks)*8+len(p.Effects)*8)
	for _, track := range p.Tracks {
		kind := track.Kind
		if kind == "piano" && !isModeledPiano(p, kind) {
			kind = "instrument"
		}
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope != "track" || !descriptorApplies(descriptor.ID, kind) {
				continue
			}
			value := trackParamValue(track, descriptor)
			addresses = append(addresses, ParamAddress{Address: track.ID + "." + descriptor.Path, Param: descriptor.ID, Track: track.ID, Value: value})
		}
	}
	for _, effect := range p.Effects {
		if MasterHasInsert(p, effect.ID) {
			continue
		}
		kind := semanticEffectKind(effect)
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope != "global" || !strings.HasPrefix(descriptor.ID, "fx."+kind+".") {
				continue
			}
			suffix := strings.TrimPrefix(descriptor.Path, kind+".")
			value := defaultParamValue(descriptor)
			if descriptor.ID == "fx.delay.time" && p.TempoMilli > 0 {
				// The default source division is 1/8, which lasts half a beat.
				value = 30_000_000 / float64(p.TempoMilli)
			}
			if source, ok := effect.Params[descriptor.Source]; ok {
				if descriptor.ID == "fx.delay.time" && source.Number == nil {
					value = delayDivisionMilliseconds(source.Text, p.TempoMilli)
				} else if descriptor.ID == "fx.comp.makeup" && source.Text == "auto" {
					value = automaticMakeup(effect.Params)
				} else {
					value = registryValue(descriptor, source)
				}
			} else if descriptor.ID == "fx.comp.makeup" {
				value = automaticMakeup(effect.Params)
			} else if descriptor.ID == "fx.comp.sidechain" {
				value = "music"
			}
			addresses = append(addresses, ParamAddress{Address: effect.ID + "." + suffix, Param: descriptor.ID, Value: value})
		}
	}
	busValues := map[string]Mixer{"music": {GainDB: -3, Bus: "music"}, "sfx": {GainDB: 0, Bus: "sfx"}}
	for _, bus := range p.Buses {
		busValues[bus.ID] = bus.Mixer
	}
	for _, busID := range []string{"music", "sfx"} {
		mixer := busValues[busID]
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope == "bus" {
				addresses = append(addresses, ParamAddress{Address: busID + "." + descriptor.Path, Param: descriptor.ID, Value: mixerParamValue(mixer, descriptor)})
			}
		}
	}
	if p.Master != nil {
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope == "master" {
				addresses = append(addresses, ParamAddress{Address: "master." + descriptor.Path, Param: descriptor.ID, Value: mixerParamValue(p.Master.Mixer, descriptor)})
			}
		}
	}
	for _, descriptor := range paramdefs.Registry {
		if descriptor.Scope == "global" && (descriptor.Path == "tempo" || descriptor.Path == "transpose") {
			value := descriptor.Default
			if descriptor.Path == "tempo" {
				value = float64(p.TempoMilli) / 1000
			}
			addresses = append(addresses, ParamAddress{Address: descriptor.Path, Param: descriptor.ID, Value: value})
		}
	}
	return addresses
}

func automaticMakeup(values map[string]Value) float64 {
	threshold, ratio := -18.0, 4.0
	if value, ok := values["threshold"]; ok && value.Number != nil {
		threshold = *value.Number
	}
	if value, ok := values["ratio"]; ok && value.Number != nil {
		ratio = *value.Number
	}
	return -(threshold * (1 - 1/ratio)) / 2
}

func delayDivisionMilliseconds(division string, tempoMilli int) float64 {
	beats := 0.0
	switch division {
	case "1/32":
		beats = .125
	case "1/16":
		beats = .25
	case "1/16T":
		beats = 1.0 / 6
	case "1/16.":
		beats = .375
	case "1/8":
		beats = .5
	case "1/8T":
		beats = 1.0 / 3
	case "1/8.":
		beats = .75
	case "3/16":
		beats = .75
	case "1/4":
		beats = 1
	case "1/4.":
		beats = 1.5
	case "1/2":
		beats = 2
	}
	if beats == 0 || tempoMilli <= 0 {
		return 250
	}
	return beats * 60_000_000 / float64(tempoMilli)
}

func descriptorApplies(id, trackKind string) bool {
	if strings.HasPrefix(id, "piano.") {
		return trackKind == "piano"
	}
	if strings.HasPrefix(id, "acid.") {
		return trackKind == "acid"
	}
	if strings.HasPrefix(id, "drum.") {
		return trackKind == "drums"
	}
	return strings.HasPrefix(id, "mix.")
}

func trackParamValue(track Track, descriptor paramdefs.Descriptor) any {
	switch descriptor.ID {
	case "mix.gain":
		return track.Mixer.GainDB
	case "mix.pan":
		return track.Mixer.Pan
	case "mix.send_a":
		return track.Mixer.SendA
	case "mix.send_b":
		return track.Mixer.SendB
	case "mix.mute":
		if track.Mixer.Mute {
			return float64(1)
		}
		return float64(0)
	case "mix.solo":
		if track.Mixer.Solo {
			return float64(1)
		}
		return float64(0)
	case "mix.send_pre":
		if track.Mixer.SendPre {
			return float64(1)
		}
		return float64(0)
	case "mix.bus":
		return track.Mixer.Bus
	case "mix.insert":
		if len(track.Mixer.Inserts) > 0 {
			return track.Mixer.Inserts[0]
		}
		return track.Mixer.Insert
	}
	if value, ok := track.Params[descriptor.Source]; ok {
		return registryValue(descriptor, value)
	}
	return defaultParamValue(descriptor)
}

func mixerParamValue(mixer Mixer, descriptor paramdefs.Descriptor) any {
	switch descriptor.Path {
	case "level":
		return mixer.GainDB
	case "pan":
		return mixer.Pan
	case "mute":
		if mixer.Mute {
			return float64(1)
		}
		return float64(0)
	case "solo":
		if mixer.Solo {
			return float64(1)
		}
		return float64(0)
	case "insert":
		if len(mixer.Inserts) > 0 {
			return mixer.Inserts[0]
		}
		return "none"
	case "out":
		if mixer.Out != nil {
			return *mixer.Out
		}
		return mixer.Bus
	default:
		return float64(0)
	}
}

func defaultParamValue(descriptor paramdefs.Descriptor) any {
	if (descriptor.Curve == "enum" || descriptor.Curve == "toggle") && int(descriptor.Default) >= 0 && int(descriptor.Default) < len(descriptor.Values) {
		if descriptor.Curve == "enum" {
			return descriptor.Values[int(descriptor.Default)]
		}
	}
	return descriptor.Default
}

func registryValue(descriptor paramdefs.Descriptor, value Value) any {
	if value.Number != nil {
		return *value.Number
	}
	if value.Text == "off" && descriptor.Off {
		return nil
	}
	if descriptor.Curve == "toggle" {
		if value.Text == "true" || value.Text == "on" {
			return float64(1)
		}
		return float64(0)
	}
	return value.Text
}

func LookupParamDescriptor(id string) (paramdefs.Descriptor, bool) {
	for _, descriptor := range paramdefs.Registry {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return paramdefs.Descriptor{}, false
}

func ParamAddressByName(p *Project, address string) (ParamAddress, error) {
	if _, err := ResolveParameterPath(p, address); err != nil {
		return ParamAddress{}, err
	}
	for _, candidate := range ParamAddresses(p) {
		if candidate.Address == address {
			return candidate, nil
		}
	}
	return ParamAddress{}, fmt.Errorf("unknown parameter address %q", address)
}
