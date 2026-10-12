package paramdefs

// Lookup returns the registry descriptor with the given ID.
func Lookup(id string) (Descriptor, bool) {
	for _, descriptor := range Registry {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// HasVoice reports whether the registry descriptor applies to a voice family.
// Compiler path resolution and live preview admission share this rule.
// A keyboard family ("ep", "clav", "organ", "fm", "analog", "strings") also
// takes every row listed for "piano", which holds the controls all keyboards
// share, such as sustain and the mixer.
func HasVoice(descriptor Descriptor, kind string) bool {
	family := false
	switch kind {
	case "ep", "clav", "organ", "fm", "analog", "strings":
		family = true
	}
	for _, voice := range descriptor.Voices {
		if voice == kind || family && voice == "piano" {
			return true
		}
	}
	return false
}
