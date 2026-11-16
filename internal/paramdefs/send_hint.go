package paramdefs

import (
	"strings"

	"m31labs.dev/cicada/kernel/dsp/detmath"
)

// SendTarget resolves an effect-name alias for a track send. Built-in send
// paths always address their respective returns.
func SendTarget(setting string, kind func(effect string) (string, bool)) (path string, ok bool, refusal string) {
	name, isSend := strings.CutPrefix(setting, "send.")
	if !isSend || name == "delay" || name == "reverb" {
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
	return "", false, "send." + name + " names effect " + name + ", a " + effectKind + ", which is not a send return; only a delay, tape or reverb effect takes sends (send.delay and send.reverb, or send.<name> with that effect's name)"
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

// LaterReverbSendMessage explains a track path send.<name> where name is a
// reverb return after the first (return slots C to H). Its send has no
// parameter path yet, and send.reverb is not it: send.reverb is the send to
// the first reverb (slot B). An agent follows a hint literally, so the message
// names what is addressable and what is not instead of pointing at send.reverb.
func LaterReverbSendMessage(path, name, first string) string {
	return "parameter path " + path + " names the send to reverb " + name + ", a return after the first (slots C to H); its level has no path yet, so scenes, automate, routes and macros cannot move it. " +
		"send.reverb is the send to " + first + ", the first reverb, not to " + name + ". Set the send to " + name + " on the track's send line"
}

// SendPathReturns maps each segment of a track's built-in send paths
// (send.delay, send.reverb) to the parameter that path addresses, from the
// registry, so the names a return may not take follow the paths the resolvers
// accept.
func SendPathReturns() map[string]string {
	segments := map[string]string{}
	for _, descriptor := range Registry {
		if descriptor.Scope != "track" {
			continue
		}
		if segment, ok := strings.CutPrefix(descriptor.Path, "send."); ok {
			segments[segment] = descriptor.ID
		}
	}
	return segments
}

// ReturnNameCollision explains why a return effect of kind (delay, tape or
// reverb) may not be named name, or returns "" when it may. A built-in send
// path <track>.send.<segment> addresses one return: send.delay the delay or
// tape return (slot A), send.reverb the first reverb (slot B). Another return
// with that name would make one path mean two sends: scenes and automate
// would move the built-in one, and setparam, which names a send by its
// effect, the other. first reports whether the effect is the first reverb.
func ReturnNameCollision(name, kind string, first bool) string {
	id, ok := SendPathReturns()[name]
	if !ok {
		return ""
	}
	switch {
	case id == "mix.send_a" && (kind == "delay" || kind == "tape"):
		return ""
	case id == "mix.send_b" && kind == "reverb" && first:
		return ""
	}
	target := "the delay or tape return"
	if id == "mix.send_b" {
		target = "the first reverb"
	}
	return "a " + kind + " return cannot be named " + name + ": the path <track>.send." + name + " is the send to " + target +
		", so a scene or automate lane on it would move that send, not this one; name it something else, such as " + name + "2"
}
