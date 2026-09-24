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
		case "send_a", "send_b", "send_pre", "mute", "solo", "bus":
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
	if mixer.SendA != 0 || mixer.SendB != 0 || mixer.SendPre || mixer.Solo || mixer.Bus != "music" {
		return fmt.Errorf("sends, solo, and non-music buses are reserved for M1")
	}
	if mixer.Insert != "none" && mixer.Insert != "drive" {
		return fmt.Errorf("insert must be drive or none")
	}
	return nil
}
