package phrase

import (
	"fmt"
	"strconv"
	"strings"
)

var keyNames = [...]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}
var scaleNames = [...]string{"minor", "phrygian", "dorian", "harmonic", "pent", "major", "mixo", "blues"}
var classNames = [...]string{"1", "3", "4", "5", "7", "1"}

func sourceForBar(notes []noteState, p normalizedParams) (string, error) {
	var source strings.Builder
	fmt.Fprintf(&source, "cicada 1\n\ntitle \"Generated phrase\"\ntempo 130\nkey %s %s\nseed %d\n\n", keyNames[p.Key], scaleNames[p.Scale], uint32(p.Seed))
	source.WriteString("track bass acid {}\n\n")
	fmt.Fprintf(&source, "pattern bass-a acid steps=%d swing=%s gate=%d {\n  ", len(notes), strconv.FormatFloat(float64(p.SwingPercent100)/100, 'f', -1, 64), p.GatePercent)
	for index, note := range notes {
		if index > 0 {
			source.WriteByte(' ')
		}
		token, err := noteToken(note, p)
		if err != nil {
			return "", fmt.Errorf("step %d: %w", index, err)
		}
		source.WriteString(token)
	}
	source.WriteString("\n}\n\nscene main { bass=bass-a }\nsong { main }\n")
	return source.String(), nil
}

func noteToken(note noteState, p normalizedParams) (string, error) {
	if !note.active {
		return ".", nil
	}
	if note.tie {
		return "-", nil
	}
	root := 12*(int(p.RootOctave)+1) + int(p.Key)
	base := 0
	name := classNames[note.class]
	switch note.class {
	case classThree, classFour, classFive:
		base = classOffsets[p.Scale][note.class]
	case classSeven:
		base = 10
		if p.Scale == HarmonicMinor || p.Scale == Major {
			base = 11
		}
	case classRoot, classOctave:
	default:
		return "", fmt.Errorf("invalid pitch class")
	}
	if p.Scale == Blues && note.class == classFour && (note.note-root+120)%12 == 6 {
		name, base = "4#", 6
	}
	delta := note.note - root - base
	if delta%12 != 0 {
		return "", fmt.Errorf("pitch %d does not match class %s", note.note, name)
	}
	for delta > 0 {
		name += "'"
		delta -= 12
	}
	for delta < 0 {
		name += ","
		delta += 12
	}
	if note.accent {
		name += "^"
	}
	if note.slide {
		name += "~"
	}
	return name, nil
}
