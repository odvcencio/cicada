package paramdefs

import (
	"strings"

	"m31labs.dev/cicada/kernel/dsp/detmath"
)

// SendTarget resolves the setting of a track path that names a send. A
// declared effect name wins over the return keywords, as in a track's send
// line (send hall = -6dB): send.<name> of a declared delay or tape is
// send.delay, of a declared reverb send.reverb, even when the name is delay
// or reverb itself, so with fx reverb delay {} the path send.reverb is the
// delay's send. A declared effect that is not a send return is refused with
// refusal. Any other setting stays as written (ok false), and send.delay and
// send.reverb then name the returns by kind. kind reports a declared effect's
// kind.
func SendTarget(setting string, kind func(effect string) (string, bool)) (path string, ok bool, refusal string) {
	name, isSend := strings.CutPrefix(setting, "send.")
	if !isSend || strings.Contains(name, ".") {
		return "", false, ""
	}
	effectKind, declared := kind(name)
	if !declared {
		return "", false, ""
	}
	switch effectKind {
	case "delay", "tape":
		return "send.delay", true, ""
	case "reverb":
		return "send.reverb", true, ""
	}
	return "", false, "send." + name + " names effect " + name + ", a " + effectKind + ", which is not a send return; only a delay, tape or reverb effect takes sends (send.delay and send.reverb, or send.<name> with that effect's name), and a declared effect name wins over the return keywords delay and reverb"
}

// SendTargetNote explains a send path whose declared effect name is the other
// return's keyword, such as send.reverb when fx reverb delay {} declares a
// delay named reverb, for a diagnostic about that path. It is "" for any
// other setting.
func SendTargetNote(setting string, kind func(effect string) (string, bool)) string {
	name, isSend := strings.CutPrefix(setting, "send.")
	if !isSend || name != "delay" && name != "reverb" {
		return ""
	}
	effectKind, declared := kind(name)
	if !declared || effectKind == name || name == "delay" && effectKind == "tape" {
		return ""
	}
	return "send." + name + " is the send to effect " + name + ", a " + effectKind + ": a declared effect name wins over the return keyword " + name
}

// IsTrackSend reports whether d is a track's send to the delay or the reverb
// return. Its value is a linear gain from 0 to 1, or a level in dB from
// SendMinDB to SendMaxDB, which SendGainFromDB converts.
func IsTrackSend(d Descriptor) bool {
	return d.Scope == "track" && (d.ID == "mix.send_a" || d.ID == "mix.send_b")
}

// SendMinDB and SendMaxDB bound a send level written in dB, as a track
// block's send line takes it.
const (
	SendMinDB = -60
	SendMaxDB = 0
)

// SendGainFromDB converts a send level in dB to the linear gain the mixer
// uses. A track block's send line, a scene setting and an automation point
// share it, so -6dB is the same gain in all three. detmath.Pow gives the same
// bits on every architecture; the mixers use the gain as a float32, which
// equals math.Pow's for every level from -60dB to +6dB in 0.01dB steps.
func SendGainFromDB(db float64) float64 {
	return detmath.Pow(10, db/20)
}
