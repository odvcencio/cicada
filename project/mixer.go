package project

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/notation"
)

// CompileMixerParams lowers track controls. `off` is the source
// spelling for a silent fader because the v1 numeric token has no infinity.
func CompileMixerParams(track notation.Track) (Mixer, error) {
	mixer := defaultMixer()
	for _, param := range track.Params {
		switch param.Name {
		case "level":
			if param.Value == "off" {
				mixer.Mute = true
				continue
			}
			value, unit, err := parseBaseValue(param.Value)
			if err != nil || unit != "db" {
				return mixer, fmt.Errorf("level requires dB or off")
			}
			mixer.GainDB = value
		case "pan":
			value, unit, err := parseBaseValue(param.Value)
			if err != nil || unit != "unit" {
				return mixer, fmt.Errorf("pan requires a unitless value")
			}
			mixer.Pan = value
		case "insert":
			if param.Value != "none" && param.Value != "drive" {
				return mixer, fmt.Errorf("insert must name the declared drive effect or none")
			}
			mixer.Insert = param.Value
		case "send_a":
			value, unit, err := parseBaseValue(param.Value)
			if err != nil || unit != "unit" {
				return mixer, fmt.Errorf("send_a requires a unitless value")
			}
			mixer.SendA = value
		case "send_b":
			value, unit, err := parseBaseValue(param.Value)
			if err != nil || unit != "unit" {
				return mixer, fmt.Errorf("send_b requires a unitless value")
			}
			mixer.SendB = value
		case "send_pre":
			if param.Value != "true" && param.Value != "false" {
				return mixer, fmt.Errorf("send_pre requires true or false")
			}
			mixer.SendPre = param.Value == "true"
		case "bus":
			if param.Value != "music" && param.Value != "sfx" {
				return mixer, fmt.Errorf("bus requires music or sfx")
			}
			mixer.Bus = param.Value
		case "mute", "solo":
			return mixer, fmt.Errorf("mixer parameter %s is reserved for M1", param.Name)
		}
	}
	return mixer, validateMixer(mixer)
}

func validateMixer(mixer Mixer) error {
	if math.IsNaN(mixer.GainDB) || math.IsInf(mixer.GainDB, 0) || mixer.GainDB < -60 || mixer.GainDB > 6 || math.IsNaN(mixer.Pan) || math.IsInf(mixer.Pan, 0) || mixer.Pan < -1 || mixer.Pan > 1 {
		return fmt.Errorf("dry mixer level must be -60..+6 dB and pan -1..1")
	}
	if mixer.Mute && mixer.GainDB != defaultMixer().GainDB {
		return fmt.Errorf("off level must use the default stored gain")
	}
	if math.IsNaN(mixer.SendA) || math.IsInf(mixer.SendA, 0) || mixer.SendA < 0 || mixer.SendA > 1 {
		return fmt.Errorf("send_a must be 0 to 1")
	}
	if math.IsNaN(mixer.SendB) || math.IsInf(mixer.SendB, 0) || mixer.SendB < 0 || mixer.SendB > 1 {
		return fmt.Errorf("send_b must be 0 to 1")
	}
	if mixer.Solo || mixer.Bus != "music" && mixer.Bus != "sfx" {
		return fmt.Errorf("solo is reserved and bus must be music or sfx")
	}
	if mixer.SendPre && mixer.SendA == 0 && mixer.SendB == 0 {
		return fmt.Errorf("send_pre requires a nonzero send")
	}
	if mixer.Insert != "none" && mixer.Insert != "drive" {
		return fmt.Errorf("insert must be drive or none")
	}
	return nil
}
