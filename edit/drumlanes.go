package edit

// GMDrumLane returns Cicada's lane index for the supported General MIDI
// percussion notes. Other pitches are not drum triggers.
func GMDrumLane(note int) (uint16, bool) {
	switch note {
	case 36:
		return 0, true // bd
	case 37:
		return 5, true // rs
	case 38:
		return 1, true // sd
	case 39:
		return 4, true // cp
	case 41, 43:
		return 6, true // lt
	case 42:
		return 2, true // ch
	case 45, 47:
		return 7, true // mt
	case 46:
		return 3, true // oh
	case 48, 50:
		return 8, true // ht
	case 49, 57:
		return 10, true // cy
	case 56:
		return 9, true // cb
	default:
		return 0, false
	}
}

var drumLaneOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}

func drumLaneName(index uint16) string {
	if int(index) >= len(drumLaneOrder) {
		return ""
	}
	return drumLaneOrder[index]
}
