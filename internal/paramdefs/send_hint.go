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

// LaterReverbSendMessage explains a track path send.<name> where name is a
// reverb return after the first (return slots C to H). Its send has no
// parameter path yet, and send.reverb is not it: send.reverb is the send to
// the first reverb (slot B). An agent follows a hint literally, so the message
// names what is addressable and what is not instead of pointing at send.reverb.
func LaterReverbSendMessage(path, name, first string) string {
	return "parameter path " + path + " names the send to reverb " + name + ", a return after the first (slots C to H); its level has no path yet, so scenes, automate, routes and macros cannot move it. " +
		"send.reverb is the send to " + first + ", the first reverb, not to " + name + ". Set the send to " + name + " on the track's send line"
}
