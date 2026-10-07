// Package keyboard defines stable patch IDs and score parameters for the optional keys module.
package keyboard

import "fmt"

var Names = [...]string{"tine_ep", "tine_bell", "tine_bark", "tine_tremolo", "reed_ep", "reed_tremolo", "clav", "clav_muted", "clav_hollow", "tonewheel_organ", "organ_jazz", "organ_full", "organ_soft", "fm_ep", "bell_keys", "fm_bass", "brass_stab", "soft_pad", "poly_keys", "sync_lead", "string_machine"}

func ID(name string) uint8 {
	for n, s := range Names {
		if name == s {
			return uint8(n + 1)
		}
	}
	return 0
}
func Name(id uint8) string {
	if id == 0 || int(id) > len(Names) {
		return ""
	}
	return Names[id-1]
}

// Parameter uses seconds, Hz, cents or dB when Unit names them. The empty unit
// is a normalized number. Integer parameters reject fractional values.
type Parameter struct {
	Name     string
	Index    int
	Min, Max float32
	Unit     string
	Integer  bool
}

func param(name string, index int, lo, hi float32, unit string, integer bool) Parameter {
	return Parameter{name, index, lo, hi, unit, integer}
}
func Parameters(name string) []Parameter {
	id := ID(name)
	if id == 0 {
		return nil
	}
	p := []Parameter{param("sustain", 0, 0, 1, "", false), param("voices", 127, 1, 8, "", true)}
	add := func(names []string, lo, hi []float32, units map[int]string, integers map[int]bool) {
		for j, n := range names {
			p = append(p, param(n, j+1, lo[j], hi[j], units[j+1], integers[j+1]))
		}
	}
	switch {
	case id <= 6:
		add([]string{"pickup_position", "pickup_distance", "hammer_felt", "decay", "release", "drive", "tremolo", "tremolo_rate", "auto_pan", "gain", "oversample"}, []float32{0, .2, 0, .3, .02, 0, 0, .1, 0, 0, 2}, []float32{1, 2, 1, 20, 2, 1, 1, 12, 1, 2, 4}, map[int]string{4: "s", 5: "s", 8: "hz"}, map[int]bool{11: true})
	case id <= 9:
		add([]string{"pickup", "string_mute", "tangent", "click", "drive", "gain"}, []float32{0, 0, .05, 0, 0, 0}, []float32{3, 1, .45, 1, 1, 2}, nil, map[int]bool{1: true})
	case id <= 13:
		for n := 0; n < 9; n++ {
			p = append(p, param(fmt.Sprintf("drawbar%d", n+1), n+1, 0, 8, "", true))
		}
		names := []string{"percussion", "percussion_fast", "percussion_soft", "scanner", "click", "leakage", "crosstalk", "drive", "rotary_mix", "mic_spread", "rotary_fast", "gain"}
		for j, n := range names {
			hi := float32(1)
			if j == 0 {
				hi = 2
			}
			if j == 3 {
				hi = 6
			}
			if j == 11 {
				hi = 2
			}
			p = append(p, param(n, j+10, 0, hi, "", j <= 3 || j == 10))
		}
	case id <= 16:
		p = append(p, param("gain", 1, .001, 2, "", false), param("stereo_spread", 2, 0, 1, "", false))
		fields := []string{"ratio", "detune", "level", "velocity", "key_tracking", "pan", "feedback", "attack", "decay", "amp_sustain", "release", "output"}
		lo := []float32{.125, -50, 0, 0, 0, -1, 0, 0, 0, 0, .005, 0}
		hi := []float32{32, 50, 8, 1, 1, 1, 2, 5, 30, 1, 30, 1}
		for op := 0; op < 6; op++ {
			for j, n := range fields {
				unit := ""
				if j == 1 {
					unit = "cents"
				}
				if j == 7 || j == 8 || j == 10 {
					unit = "s"
				}
				p = append(p, param(fmt.Sprintf("op%d_%s", op+1, n), 3+op*12+j, lo[j], hi[j], unit, false))
			}
		}
		index := 75
		for dst := 0; dst < 5; dst++ {
			for src := dst + 1; src < 6; src++ {
				p = append(p, param(fmt.Sprintf("route%d_%d", src+1, dst+1), index, 0, 4, "", false))
				index++
			}
		}
	case id <= 20:
		add([]string{"saw", "pulse", "pulse_width", "pwm", "pwm_rate", "sub", "detune", "drift", "sync", "cutoff", "resonance", "key_track", "filter_env", "drive", "attack", "decay", "amp_sustain", "release", "velocity", "chorus", "output", "filter"}, []float32{0, 0, .1, 0, .01, 0, 0, 0, 1, 20, 0, 0, 0, 0, .001, .01, 0, .01, 0, 0, -60, 0}, []float32{1, 1, .9, .35, 10, 1, 30, 10, 8, 18000, .95, 1, 6, 1, 10, 10, 1, 10, 1, 1, 6, 1}, map[int]string{5: "hz", 7: "cents", 8: "cents", 10: "hz", 15: "s", 16: "s", 18: "s", 21: "db"}, map[int]bool{22: true})
	default:
		add([]string{"octave16", "octave8", "octave4", "attack", "release", "cutoff", "velocity", "ensemble", "ensemble_rate", "output"}, []float32{0, 0, 0, .001, .01, 200, 0, 0, .1, -60}, []float32{1, 1, 1, 10, 10, 16000, 1, 1, 3, 6}, map[int]string{4: "s", 5: "s", 6: "hz", 10: "db"}, nil)
	}
	return p
}
