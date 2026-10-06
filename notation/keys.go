package notation

import "m31labs.dev/cicada/kernel/voice/keyboard"

func notationModeledKeys(s *Score, kind string) bool {
	if keyboard.ID(kind) == 0 || scoreHasSampler(s, kind) {
		return false
	}
	for _, i := range s.Instruments {
		if i.Name == kind {
			return false
		}
	}
	for _, k := range s.Kits {
		if k.Name == kind {
			return false
		}
	}
	return true
}
func validKeysParam(kind, name string) bool {
	if name == "octave" {
		return true
	}
	for _, p := range keyboard.Parameters(kind) {
		if name == p.Name {
			return true
		}
	}
	return false
}
