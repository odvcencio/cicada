//go:build !tinygo

package keyboard

import (
	"fmt"
	"strconv"
	"strings"
)

// Meaning says in one line what a keyboard patch control does, for the
// control catalog (cicada params and cicada explain). It is host metadata: no
// WebAssembly kernel build compiles this file. A test fails when a control of
// Parameters has no meaning here or a meaning names no control.
//
// An FM operator's role depends on the patch's routing; the catalog adds it.
func Meaning(patch, control string) string {
	if text, ok := sharedMeanings[control]; ok {
		return text
	}
	family := FamilyOf(ID(patch)).String()
	if text, ok := familyMeanings[family][control]; ok {
		return text
	}
	if family == "organ" && strings.HasPrefix(control, "drawbar") {
		n, err := strconv.Atoi(strings.TrimPrefix(control, "drawbar"))
		if err == nil && n >= 1 && n <= len(drawbarFeet) {
			return fmt.Sprintf("level of drawbar %d, the %s pipe (%s); 0 is silent and 8 is full", n, drawbarFeet[n-1], drawbarRole[n-1])
		}
	}
	if family == "fm" {
		var op, src, dst int
		var field string
		if n, err := fmt.Sscanf(control, "route%d_%d", &src, &dst); err == nil && n == 2 && strings.Count(control, "_") == 1 {
			return fmt.Sprintf("how strongly operator %d modulates operator %d; 0 removes the connection", src, dst)
		}
		if n, err := fmt.Sscanf(control, "op%d_", &op); err == nil && n == 1 {
			field = strings.TrimPrefix(control, fmt.Sprintf("op%d_", op))
			if text, ok := fmOperatorMeanings[field]; ok {
				return fmt.Sprintf(text, op)
			}
		}
	}
	return ""
}

var sharedMeanings = map[string]string{
	"sustain": "damper pedal, a switch at 0.5: from 0.5 up (or on) released notes keep sounding until it drops below 0.5; below 0.5 (or off) they end at release",
	"voices":  "how many notes can sound at once; a new note past the limit steals the oldest",
}

// drawbarFeet are the drawbar pitches in order, from the organ's own table.
var drawbarFeet = [9]string{"16'", "5 1/3'", "8'", "4'", "2 2/3'", "2'", "1 3/5'", "1 1/3'", "1'"}

var drawbarRole = [9]string{
	"one octave below the key", "a fifth above the key", "the key's own pitch",
	"one octave above", "an octave and a fifth above", "two octaves above",
	"two octaves and a major third above", "two octaves and a fifth above", "three octaves above",
}

var familyMeanings = map[string]map[string]string{
	"ep": {
		"pickup_position": "offset of the pickup from the tine's rest position; 0.5 is centered, values toward 0 or 1 move it off center and change the overtone balance",
		"pickup_distance": "gap between pickup and tine; a smaller gap makes the pickup more nonlinear, for a stronger, more distorted bark",
		"hammer_felt":     "hammer felt softness: 0 strikes hardest, with the loudest click, and 1 strikes softest",
		"decay":           "how long a held note rings: seconds for the fundamental to fall 60 dB at middle C",
		"release":         "how long a note takes to stop after the key is released",
		"drive":           "preamp saturation after the pickup; higher values add harmonics and compress peaks",
		"tremolo":         "depth of the built-in amplitude tremolo; 0 is off",
		"tremolo_rate":    "speed of the tremolo and auto-pan, in cycles per second",
		"auto_pan":        "depth of the stereo auto-pan that moves with the tremolo; 0 is off",
		"gain":            "output level of the voice as a linear multiplier; 1 is unity",
		"oversample":      "internal oversampling factor of the pickup model: 2 or 4 (3 is refused); 4 lowers aliasing at more CPU",
	},
	"clav": {
		"pickup":      "which pickup is heard: bridge (bright, thin), neck (round), both summed, or their difference (hollow)",
		"string_mute": "yarn damping on the strings; higher values shorten the notes and dull their overtones",
		"tangent":     "where along the string the key's tangent strikes, as a fraction of its length; it sets which string modes are excited",
		"click":       "level of the mechanical key click at each strike",
		"drive":       "preamp saturation; higher values add harmonics and compress peaks",
		"gain":        "output level of the voice as a linear multiplier; 1 is unity",
	},
	"organ": {
		"percussion":      "percussive attack on the second or third harmonic, or off",
		"percussion_fast": "how quickly the percussion decays: slow or fast",
		"percussion_soft": "percussion level: normal or soft",
		"scanner":         "scanner vibrato or chorus: off, vibrato v1 to v3, chorus c1 to c3 (higher numbers are deeper)",
		"click":           "level of the key-contact click at note start",
		"leakage":         "level of the tonewheel leakage hum under the drawbar tone",
		"crosstalk":       "level of neighboring tonewheels bleeding into each note",
		"drive":           "preamp saturation; higher values add harmonics",
		"rotary_mix":      "how much of the sound passes through the rotary speaker instead of directly",
		"mic_spread":      "stereo spread of the two rotary speaker microphones",
		"rotary_fast":     "rotary speaker speed: slow (chorale) or fast (tremolo); changing it ramps the motors",
		"gain":            "output level of the voice as a linear multiplier; 1 is unity",
	},
	"fm": {
		"gain":          "output level of the whole patch as a linear multiplier",
		"stereo_spread": "width of the stereo image: scales each carrier's pan and a gentle pan by key position; 0 is mono",
	},
	"analog": {
		"saw":         "relative level of the saw oscillator; saw, pulse and half the sub are scaled to a constant sum, and saw and pulse cannot both be 0",
		"pulse":       "relative level of the pulse oscillator; saw, pulse and half the sub are scaled to a constant sum, and saw and pulse cannot both be 0",
		"pulse_width": "duty cycle of the pulse wave; 0.5 is square, values away from it sound thinner",
		"pwm":         "depth of pulse-width modulation by its own oscillator",
		"pwm_rate":    "speed of the pulse-width modulation",
		"sub":         "relative level of the sub oscillator one octave below the key, in the same constant-sum mix as saw and pulse",
		"detune":      "spread between the detuned oscillators; more cents sound wider and more chorused",
		"drift":       "random slow pitch drift of each voice, as an analog oscillator wanders",
		"sync":        "hard-sync ratio of the slave oscillator to the master; 1 is no sync, higher values add a tearing, vocal edge",
		"cutoff":      "resting cutoff frequency of the voice filter, before key tracking and the filter envelope",
		"resonance":   "emphasis at the filter cutoff; high values ring and whistle",
		"key_track":   "how much the filter cutoff follows the key; 1 opens it fully for higher notes",
		"filter_env":  "how far the filter envelope opens the cutoff at each note, in octaves",
		"drive":       "saturation in the filter; higher values add harmonics",
		"attack":      "time the amplitude envelope takes to reach full level",
		"decay":       "time the amplitude envelope takes to fall 40 dB toward the sustain level",
		"amp_sustain": "level the amplitude envelope holds while the key is down, as a fraction of full level",
		"release":     "time a note takes to fade 40 dB after the key is released",
		"velocity":    "how much key velocity changes the level; 0 plays every note at the same level",
		"chorus":      "amount of the built-in chorus",
		"output":      "output level of the voice",
		"filter":      "filter model: ladder (4-pole low-pass) or state_variable",
	},
	"strings": {
		"octave16":      "level of the rank one octave below the key",
		"octave8":       "level of the rank at the key's own pitch",
		"octave4":       "level of the rank one octave above the key",
		"attack":        "time a note takes to swell to full level",
		"release":       "time a note takes to fade 40 dB after the key is released",
		"cutoff":        "cutoff frequency of the voice's low-pass filter",
		"velocity":      "how much key velocity changes the level; 0 plays every note at the same level",
		"ensemble":      "depth of the three-phase ensemble chorus",
		"ensemble_rate": "speed of the ensemble chorus modulation, as a scale of its three rates",
		"output":        "output level of the voice",
	},
}

// fmOperatorMeanings take the operator number. Ratio, detune and envelope
// fields act on every operator; level reads differently for carriers and
// modulators, so the catalog adds the operator's role in each patch.
var fmOperatorMeanings = map[string]string{
	"ratio":        "frequency of operator %d as a multiple of the played pitch",
	"detune":       "fine tuning of operator %d away from its ratio",
	"level":        "level of operator %d: its loudness when it is a carrier, its modulation depth (in radians) when it modulates another operator",
	"velocity":     "how much key velocity changes operator %d's level; 0 is constant, 1 is fully velocity sensitive",
	"key_tracking": "how much operator %d's decay and release shorten toward the treble",
	"pan":          "stereo position of operator %d when it is a carrier, scaled by stereo_spread",
	"feedback":     "operator %d modulating itself; higher values turn its sine toward a saw-like, brighter tone",
	"attack":       "time operator %d's envelope takes to reach full level",
	"decay":        "time operator %d's envelope takes to fall 60 dB toward its sustain level",
	"amp_sustain":  "level operator %d's envelope holds while the key is down, as a fraction of full level",
	"release":      "time operator %d's envelope takes to fall 60 dB after the key is released",
	"output":       "how much of operator %d reaches the output; above 0 makes it an audible carrier",
}

// MeaningNames lists the control names the meaning tables describe, by
// family; "" holds the controls every patch shares. The FM operator and route
// templates and the organ drawbars are left out: they cover every operator,
// route and drawbar Parameters lists.
func MeaningNames() map[string][]string {
	names := map[string][]string{}
	for name := range sharedMeanings {
		names[""] = append(names[""], name)
	}
	for family, table := range familyMeanings {
		for name := range table {
			names[family] = append(names[family], name)
		}
	}
	return names
}
