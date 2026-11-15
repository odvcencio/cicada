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
