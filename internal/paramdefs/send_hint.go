package paramdefs

import (
	"math"
	"strings"
)

// SendPathHint explains the send paths when a track path names a send by an
// effect that is not a send return. A path names a send by its return slot,
// send.delay for the delay or tape return and send.reverb for the reverb
// return, or by the declared name of that return's effect (SendAlias), as a
// track's send line does (send echo = 0.2). kind reports a declared effect's
// kind. The hint is "" when setting is not send.<effect>.
func SendPathHint(setting string, kind func(effect string) (string, bool)) string {
	name, ok := strings.CutPrefix(setting, "send.")
	if !ok {
		return ""
	}
	if _, declared := kind(name); !declared {
		return ""
	}
	return "; only the delay and reverb returns have track send paths (send.delay and send.reverb, or send.<name> for the declared delay, tape or reverb effect)"
}

// SendAlias maps send.<name>, where name is a declared delay, tape or reverb
// effect, to the send path of its return: send.delay or send.reverb. A track
// block writes a send by the effect's name (send hall = -6dB), so a scene or
// automation path may too (a.send.hall). It reports false for any other
// setting, so the return-slot spellings keep their meaning even when an effect
// is named delay or reverb.
func SendAlias(setting string, kind func(effect string) (string, bool)) (string, bool) {
	name, ok := strings.CutPrefix(setting, "send.")
	if !ok || strings.Contains(name, ".") {
		return "", false
	}
	effectKind, declared := kind(name)
	if !declared {
		return "", false
	}
	switch effectKind {
	case "delay", "tape":
		return "send.delay", true
	case "reverb":
		return "send.reverb", true
	}
	return "", false
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
// share it, so -6dB is the same gain in all three.
func SendGainFromDB(db float64) float64 {
	return math.Pow(10, db/20)
}
