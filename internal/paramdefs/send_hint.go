package paramdefs

import "strings"

// SendPathHint explains the send paths when a track path names a send by its
// effect, as a track's send line does (send echo = 0.2). A path names the
// return slot instead: send.delay for the delay or tape return, send.reverb
// for the reverb return. kind reports a declared effect's kind. The hint is
// "" when setting is not send.<effect>.
func SendPathHint(setting string, kind func(effect string) (string, bool)) string {
	name, ok := strings.CutPrefix(setting, "send.")
	if !ok {
		return ""
	}
	effectKind, declared := kind(name)
	if !declared {
		return ""
	}
	switch effectKind {
	case "delay", "tape":
		return "; a path names the send by its return, so the send to " + name + " is send.delay"
	case "reverb":
		return "; a path names the send by its return, so the send to " + name + " is send.reverb"
	}
	return "; only the delay and reverb returns have track send paths (send.delay and send.reverb)"
}

// InstrumentParamHint explains why a route, automation lane or scene setting
// cannot reach an authored instrument's own parameter: its value is fixed when
// the instrument compiles. It names the in-graph alternative.
func InstrumentParamHint(instrument, param string) string {
	return "; " + param + " is a parameter of instrument " + instrument + ", fixed when the instrument compiles, so only the track's static " + param + " setting changes it; to move it while it plays, read a modulator inside the instrument, such as " + param + " * exp2(mod(<source>))"
}
