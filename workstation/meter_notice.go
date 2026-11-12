package main

import (
	"strconv"

	"m31labs.dev/cicada/project"
)

// meterNotice tells a user of a song in a meter other than 4/4 that Studio's
// bars, grid and snapping are still drawn in 4/4 (time-signatures phase 5
// brings them to the song's meter), while typed positions and agent edits
// already use the song's meter.
func meterNotice(m *project.Meter) (string, bool) {
	if m == nil || m.Numerator == 4 && m.Denominator == 4 {
		return "", false
	}
	meter := strconv.Itoa(m.Numerator) + "/" + strconv.Itoa(m.Denominator)
	return "This song is in " + meter + ". Studio still draws bars, the grid and snapping in 4/4; positions you type in the Code view and edits through cicada intents use " + meter + ".", true
}
