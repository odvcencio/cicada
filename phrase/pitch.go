package phrase

import "fmt"

type pitchClass uint8

const (
	classRoot pitchClass = iota
	classThree
	classFour
	classFive
	classSeven
	classOctave
)

var degreeColumns = [...]pitchClass{classRoot, classFive, classSeven, classOctave, classThree, classFour}

// Rows use class order R, 3, 4, 5, 7, O; columns use R, 5, 7, O, 3, 4.
var degreeWeights = [6][6]uint8{
	{35, 20, 15, 15, 10, 5},
	{40, 20, 10, 0, 10, 20},
	{30, 30, 10, 0, 30, 0},
	{40, 0, 20, 15, 10, 15},
	{45, 15, 0, 25, 15, 0},
	{35, 20, 25, 10, 10, 0},
}

var classOffsets = [8][6]int{
	{0, 3, 5, 7, -2, 12}, // minor
	{0, 3, 5, 7, -2, 12}, // phrygian
	{0, 3, 5, 7, -2, 12}, // dorian
	{0, 3, 5, 7, -1, 12}, // harmonic minor
	{0, 3, 5, 7, -2, 12}, // minor pentatonic
	{0, 4, 5, 7, -1, 12}, // major
	{0, 4, 5, 7, -2, 12}, // mixolydian
	{0, 3, 5, 7, -2, 12}, // blues
}

type noteState struct {
	active, accent, slide, tie, raised bool
	class                              pitchClass
	note                               int
	ratchet                            uint8
}

func generateDegrees(s *stream, onsets []bool, p normalizedParams) ([]noteState, error) {
	root := 12*int(p.RootOctave+1) + int(p.Key)
	notes := make([]noteState, len(onsets))
	previous := classRoot
	first := true
	for index, active := range onsets {
		if !active {
			continue
		}
		class := classRoot
		if !first {
			column := s.pick("degree", index, degreeWeights[previous][:])
			class = degreeColumns[column]
		}
		offset := classOffsets[p.Scale][class]
		if previous == classOctave && class == classSeven {
			offset = 10
			if p.Scale == HarmonicMinor || p.Scale == Major {
				offset = 11
			}
		}
		if p.Scale == Blues && class == classFour && s.choose("degree", index, 4) == 0 {
			offset = 6
		}
		note := root + offset
		if note < 0 || note > 127 {
			return nil, fmt.Errorf("generated pitch %d is outside MIDI range", note)
		}
		notes[index] = noteState{active: true, class: class, note: note}
		previous, first = class, false
	}
	return notes, nil
}

func jumpOctaves(s *stream, notes []noteState, density uint8) {
	for index := 1; index < len(notes); index++ {
		class := notes[index].class
		if !notes[index].active || class != classRoot && class != classThree && class != classFive {
			continue
		}
		roll := s.unit24("octave", index)
		if uint64(roll) >= uint64(density)*(1<<23)/64 || notes[index].note+12 > 127 {
			continue
		}
		previous, next := adjacentOnsets(notes, index)
		if previous >= 0 && (notes[previous].raised || abs(notes[index].note+12-notes[previous].note) > 12) {
			continue
		}
		if next >= 0 && abs(notes[index].note+12-notes[next].note) > 12 {
			continue
		}
		notes[index].note += 12
		notes[index].raised = true
	}
}

func adjacentOnsets(notes []noteState, index int) (previous, next int) {
	previous, next = -1, -1
	for distance := 1; distance < len(notes); distance++ {
		candidate := (index - distance + len(notes)) % len(notes)
		if notes[candidate].active {
			previous = candidate
			break
		}
	}
	for distance := 1; distance < len(notes); distance++ {
		candidate := (index + distance) % len(notes)
		if notes[candidate].active {
			next = candidate
			break
		}
	}
	return previous, next
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
