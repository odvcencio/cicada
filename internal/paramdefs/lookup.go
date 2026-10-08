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
