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
func HasVoice(descriptor Descriptor, kind string) bool {
	for _, voice := range descriptor.Voices {
		if voice == kind {
			return true
		}
	}
	return false
}
