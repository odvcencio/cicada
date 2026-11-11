//go:build !tinygo

package paramdefs

import "strings"

// Meaning says in one line what the control of a registry, HostOnly or
// SamplerSettings row does, for the control catalog (cicada params and cicada
// explain). It is host metadata: no WebAssembly build compiles this file. A
// test fails when a row has no meaning or a meaning names no row. The
// instrument.p slots carry an authored parameter and have none.
func Meaning(id string) string {
	if text, ok := meanings[id]; ok {
		return text
	}
	if lane, field, ok := drumRow(id); ok {
		if text, ok := drumFieldMeanings[lane+"."+field]; ok {
			return text
		}
		if text, ok := drumFieldMeanings[field]; ok {
			return strings.ReplaceAll(text, "LANE", drumLaneNames[lane])
		}
	}
	return ""
}

// drumRow splits drum.<lane>.<field>.
func drumRow(id string) (lane, field string, ok bool) {
	rest, found := strings.CutPrefix(id, "drum.")
	if !found {
		return "", "", false
	}
	lane, field, ok = strings.Cut(rest, ".")
	_, known := drumLaneNames[lane]
	return lane, field, ok && known
}

var drumLaneNames = map[string]string{
	"bd": "kick", "sd": "snare", "ch": "closed hi-hat", "oh": "open hi-hat", "cp": "clap",
	"rs": "rimshot", "lt": "low tom", "mt": "mid tom", "ht": "high tom", "cb": "cowbell", "cy": "cymbal",
}

// drumFieldMeanings hold a field's meaning for every lane (LANE is replaced by
// the lane's name) or, keyed lane.field, for one lane.
var drumFieldMeanings = map[string]string{
	"level":         "level of the LANE sound in the kit",
	"pan":           "stereo position of the LANE sound, from -1 (left) to 1 (right)",
	"tune":          "pitch of the LANE sound as a ratio of its built-in tuning; 1 is unchanged",
	"decay":         "how long the LANE sound rings",
	"bd.tune":       "pitch the kick settles to, in Hz",
	"bd.sweep":      "how far above its settled pitch the kick starts, as a multiple; higher values give a stronger pitch drop",
	"bd.sweep_time": "how fast the kick's pitch drop settles",
	"bd.click":      "level of the noise click at the kick's attack",
	"bd.drive":      "saturation of the kick body; higher values add harmonics",
	"sd.tone":       "pitch of the snare's tonal body as a ratio of its built-in tuning",
	"sd.mix":        "balance between the snare's tonal body (0) and its noise rattle (1)",
	"sd.snappy":     "how long the snare's noise rattle lasts",
	"ch.tone":       "center frequency of the closed hi-hat's metallic band",
	"ch.metal":      "on switches the closed hi-hat to a more metallic tone",
	"oh.tone":       "center frequency of the open hi-hat's metallic band",
	"oh.metal":      "on switches the open hi-hat to a more metallic tone",
	"cp.tone":       "center frequency of the clap's noise band",
	"cp.spread":     "time between the clap's individual hand hits",
	"lt.sweep":      "how far the low tom's pitch drops after the hit; 0 is a steady pitch",
	"mt.sweep":      "how far the mid tom's pitch drops after the hit; 0 is a steady pitch",
	"ht.sweep":      "how far the high tom's pitch drops after the hit; 0 is a steady pitch",
	"cy.tone":       "brightness of the cymbal's noise band as a ratio of its built-in tuning",
}

var meanings = map[string]string{
	"mix.gain":      "track output level (the fader); off silences the track",
	"mix.pan":       "stereo position of the track, from -1 (left) to 1 (right)",
	"mix.send_a":    "how much of the track goes to the delay return",
	"mix.send_b":    "how much of the track goes to the reverb return",
	"mix.mute":      "silences the track while on",
	"mix.solo":      "while any track is soloed, only soloed tracks play",
	"mix.send_pre":  "takes the track's sends before its fader (on) or after it (off)",
	"mix.bus":       "the bus the track feeds: music or sfx",
	"mix.insert":    "the track's insert chain: effect names in signal order, or none",
	"mix.bus.level": "level of the bus", "mix.bus.pan": "stereo position of the bus",
	"mix.bus.mute": "silences the bus while on", "mix.bus.solo": "solos the bus while on",
	"mix.bus.insert":         "the bus insert chain: effect names in signal order, or none",
	"mix.bus.send.delay":     "how much of the bus goes to the delay return",
	"mix.bus.send.reverb":    "how much of the bus goes to the reverb return",
	"mix.bus.out":            "where the bus output goes",
	"mix.master.level":       "level of the master output",
	"mix.master.pan":         "stereo position of the master output",
	"mix.master.mute":        "silences the master output while on",
	"mix.master.solo":        "solos the master output while on",
	"mix.master.insert":      "the master insert chain: effect names in signal order, or none",
	"mix.master.send.delay":  "how much of the master goes to the delay return",
	"mix.master.send.reverb": "how much of the master goes to the reverb return",
	"mix.master.out":         "where the master output goes",
	"global.tempo":           "song tempo in beats per minute; a scene can change it at its start",
	"global.transpose":       "transposes every pitched track by this many semitones",

	"fx.drive.shape": "saturation curve: soft, hard, fold or diode",
	"fx.drive.gain":  "gain into the saturator; more gain drives it harder for more distortion",
	"fx.drive.tone":  "cutoff of the low-pass filter after the saturator; lower values darken the distortion",
	"fx.drive.mix":   "balance between driven (1) and dry (0) signal",

	"fx.delay.time":     "delay time in milliseconds, or a note division such as 1/8 or 3/16",
	"fx.delay.feedback": "how much of each echo feeds back into the delay; higher values give more repeats",
	"fx.delay.damp":     "cutoff of the low-pass filter in the feedback path; lower values darken each repeat",
	"fx.delay.pingpong": "on bounces the echoes between left and right",
	"fx.delay.width":    "stereo width of the echoes",
	"fx.delay.mix":      "level of the delay return's echoes",

	"fx.reverb.size":     "scales the size of the simulated room",
	"fx.reverb.decay":    "length of the reverb tail",
	"fx.reverb.damp":     "cutoff of the high-frequency damping in the tail; lower values darken it",
	"fx.reverb.highpass": "high-pass filter on the reverb input; higher values keep low end out of the tail",
	"fx.reverb.predelay": "gap between the dry sound and the start of the reverb",
	"fx.reverb.mix":      "level of the reverb return's tail",

	"fx.comp.detect":    "level detector: peak or rms",
	"fx.comp.threshold": "level above which the compressor reduces gain",
	"fx.comp.ratio":     "how strongly level above the threshold is reduced; 4 turns 4 dB over the threshold into 1 dB",
	"fx.comp.knee":      "width of the soft knee around the threshold",
	"fx.comp.attack":    "how quickly gain reduction starts when the level rises",
	"fx.comp.release":   "how quickly gain reduction recovers when the level falls",
	"fx.comp.makeup":    "gain added after compression, or auto",
	"fx.comp.mix":       "balance between compressed (1) and dry (0) signal, for parallel compression",
	"fx.comp.sidechain": "the track or bus whose level drives the gain reduction instead of the compressor's own input",

	"fx.eq.type":      "filter shape: peak, low_shelf, high_shelf, highpass or lowpass",
	"fx.eq.frequency": "center or corner frequency of the filter",
	"fx.eq.q":         "bandwidth of a peak or the steepness of a shelf or pass filter; higher values are narrower",
	"fx.eq.gain":      "boost (positive) or cut (negative) of a peak or shelf",

	"fx.deesser.frequency": "frequency above which sibilance is detected and reduced",
	"fx.deesser.threshold": "sibilance level above which the de-esser reduces gain",
	"fx.deesser.ratio":     "how strongly sibilance above the threshold is reduced",
	"fx.deesser.range":     "the most gain reduction the de-esser applies",
	"fx.deesser.attack":    "how quickly reduction starts on a sibilant",
	"fx.deesser.release":   "how quickly reduction recovers after a sibilant",
	"fx.deesser.mix":       "balance between processed (1) and dry (0) signal",

	"fx.chorus.voices": "number of delayed copies the chorus mixes in, 1 to 3",
	"fx.chorus.rate":   "speed of the chorus modulation",
	"fx.chorus.depth":  "how far the chorus delay times swing",
	"fx.chorus.spread": "stereo spread of the chorus copies",
	"fx.chorus.mix":    "balance between chorused (1) and dry (0) signal",

	"fx.tape.time":       "echo time of the tape delay return, in milliseconds or as a note division",
	"fx.tape.feedback":   "how much of each echo feeds back; higher values give more repeats",
	"fx.tape.wow":        "depth of slow tape speed variation, which bends the echoes' pitch",
	"fx.tape.flutter":    "depth of fast tape speed variation",
	"fx.tape.saturation": "tape saturation of the echoes; higher values add harmonics",
	"fx.tape.damp":       "cutoff of the low-pass filter on the echoes; lower values darken each repeat",

	"fx.transient.attack":    "boost (positive) or cut (negative) of note attacks",
	"fx.transient.sustain":   "boost (positive) or cut (negative) of the body after each attack",
	"fx.transient.fast":      "time constant of the fast envelope follower that finds attacks",
	"fx.transient.slow":      "time constant of the slow envelope follower that the fast one is compared with",
	"fx.transient.release":   "how quickly the shaper's gain returns to neutral",
	"fx.width.amount":        "stereo width: 0 is mono, 1 is unchanged and 2 doubles the side signal",
	"fx.width.bass_mono":     "frequency below which the master is made mono; 0 bypasses it",
	"fx.master.eq.frequency": "center or corner frequency of a master EQ filter",
	"fx.limiter.ceiling":     "highest true-peak level the limiter lets through",
	"fx.limiter.lookahead":   "how far ahead the limiter looks to catch peaks",
	"fx.limiter.release":     "how quickly the limiter's gain reduction recovers",

	"acid.tune":    "coarse pitch offset in semitones",
	"acid.fine":    "fine pitch offset in cents",
	"acid.wave":    "oscillator shape, a crossfade from saw (0) to square (1)",
	"acid.pw":      "pulse width of the square wave; 0.5 is square",
	"acid.detune":  "pitch offset of a second oscillator in cents; 0 turns it off",
	"acid.sub":     "level of a square sub oscillator one octave down",
	"acid.cutoff":  "resting cutoff frequency of the filter, before the envelope opens it",
	"acid.reso":    "filter resonance; high values give the squelch and self-oscillation",
	"acid.envmod":  "how far the filter envelope opens the cutoff on each note",
	"acid.decay":   "how long the filter envelope takes to close after each note starts",
	"acid.accent":  "how much accented steps raise the level and open the filter",
	"acid.drive":   "saturation after the filter; higher values add harmonics",
	"acid.release": "how long a note takes to stop after its gate ends",
	"acid.slide":   "glide time between slid notes",
	"acid.gate":    "default note length as a percentage of the step",
	"acid.filter":  "filter model: diode or ladder",
	"acid.savage":  "on blends in an aggressive saturation stage and raises the filter's resonance feedback by a quarter",
	"acid.octave":  "octave the pattern's note numbers play in",

	"piano.sustain": "damper pedal, continuous: 0 lets released notes be damped, 1 lifts the dampers fully, and values between damp them partly",

	"guitar.bend":         "pitch bend in semitones",
	"guitar.vibrato":      "vibrato depth in cents",
	"guitar.brightness":   "pluck brightness; higher values keep more upper harmonics",
	"guitar.damping":      "palm-mute damping of the strings; higher values shorten notes",
	"guitar.pickup":       "pickup position along the string, as a fraction of its length",
	"guitar.drive":        "amp drive of the guitar; higher values add distortion",
	"guitar.experimental": "must be on: the guitar voice is experimental and asks for this opt-in",
	"guitar.octave":       "octave the pattern's note numbers play in",

	"keys.ep.tremolo":        "depth of the electric piano's tremolo; 0 is off",
	"keys.ep.tremolo_rate":   "speed of the tremolo and auto-pan",
	"keys.ep.auto_pan":       "depth of the stereo auto-pan that moves with the tremolo",
	"keys.ep.gain":           "output level of the electric piano as a linear multiplier",
	"keys.ep.drive":          "preamp saturation of the electric piano",
	"keys.clav.gain":         "output level of the clavinet as a linear multiplier",
	"keys.clav.drive":        "preamp saturation of the clavinet",
	"keys.organ.gain":        "output level of the organ as a linear multiplier",
	"keys.organ.drive":       "preamp saturation of the organ",
	"keys.organ.rotary_mix":  "how much of the organ passes through the rotary speaker",
	"keys.organ.rotary_fast": "rotary speaker speed: slow or fast",
	"keys.fm.gain":           "output level of the FM patch as a linear multiplier",
	"keys.analog.drive":      "filter saturation of the analog keys",
	"keys.analog.output":     "output level of the analog keys",
	"keys.strings.output":    "output level of the string machine",

	"amp.drive":  "drive of the track's amp insert; needs exactly one amp insert on the track",
	"amp.tone":   "tone control of the track's amp insert; needs exactly one amp insert on the track",
	"amp.output": "output level of the track's amp insert; needs exactly one amp insert on the track",

	"chorus.rate":   "speed of the track's chorus insert; needs exactly one chorus insert on the track",
	"chorus.depth":  "depth of the track's chorus insert; needs exactly one chorus insert on the track",
	"chorus.spread": "stereo spread of the track's chorus insert; needs exactly one chorus insert on the track",
	"chorus.mix":    "dry and wet balance of the track's chorus insert; needs exactly one chorus insert on the track",

	"sampler.mode":   "oneshot plays each sample to its end; loop repeats it while the note is held",
	"sampler.voices": "how many notes the sampler can play at once",
	"sampler.root":   "the pitch the sample was recorded at, so other notes are transposed from it",
}

func init() {
	for n := 1; n <= 9; n++ {
		id := "keys.organ.drawbar" + string(rune('0'+n))
		meanings[id] = "level of organ drawbar " + string(rune('0'+n)) + ", 0 to 8"
	}
}

// MeaningIDs lists the IDs the meaning tables describe, for the test that
// keeps them free of stale entries. Drum rows are listed by lane and field.
func MeaningIDs() []string {
	var ids []string
	for id := range meanings {
		ids = append(ids, id)
	}
	for key := range drumFieldMeanings {
		lane, field, ok := strings.Cut(key, ".")
		if ok {
			ids = append(ids, "drum."+lane+"."+field)
			continue
		}
		for lane := range drumLaneNames {
			if Meaning("drum."+lane+"."+key) != "" {
				if _, found := Lookup("drum." + lane + "." + key); found {
					ids = append(ids, "drum."+lane+"."+key)
				}
			}
		}
	}
	return ids
}
