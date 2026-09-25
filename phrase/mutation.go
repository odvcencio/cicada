package phrase

import "fmt"

func mutateNotes(base []noteState, seed uint64, ops []Op, locked uint64, p normalizedParams) ([]noteState, []Draw, error) {
	notes := append([]noteState(nil), base...)
	s := newStream(seed)
	root := 12*(int(p.RootOctave)+1) + int(p.Key)
	for _, op := range ops {
		switch op.Kind {
		case NudgeDegree:
			eligible := editableOnsets(notes, locked)
			index := chooseStep(&s, "mutation", eligible)
			if index < 0 {
				continue
			}
			direction := -1
			if s.choose("mutation", index, 2) == 1 {
				direction = 1
			}
			class := pitchClass((int(notes[index].class) + direction + 6) % 6)
			basePitch := root + classOffsets[p.Scale][class]
			notes[index].note = nearestOctave(basePitch, notes[index].note)
			notes[index].class = class
			if class != classRoot && index == 0 {
				notes[index].tie = false
			}
		case ToggleAccent:
			index := chooseStep(&s, "mutation", editableOnsets(notes, locked))
			if index >= 0 {
				notes[index].accent = !notes[index].accent
			}
		case ToggleSlide:
			var eligible []int
			for index, note := range notes {
				next := (index + 1) % len(notes)
				if note.active && !note.tie && notes[next].active && allowedSlideInterval(notes[next].note-note.note) && locked&(uint64(1)<<index) == 0 && locked&(uint64(1)<<next) == 0 {
					eligible = append(eligible, index)
				}
			}
			index := chooseStep(&s, "mutation", eligible)
			if index < 0 {
				continue
			}
			next := (index + 1) % len(notes)
			if notes[index].note == notes[next].note {
				notes[index].slide = false
				notes[next].tie = !notes[next].tie
			} else {
				notes[index].slide = !notes[index].slide
			}
		case OctaveFlip:
			var eligible []int
			for _, index := range editableOnsets(notes, locked) {
				if len(octaveDirections(notes[index].note)) > 0 {
					eligible = append(eligible, index)
				}
			}
			index := chooseStep(&s, "mutation", eligible)
			if index < 0 {
				continue
			}
			directions := octaveDirections(notes[index].note)
			choice := 0
			if len(directions) == 2 {
				choice = s.choose("mutation", index, 2)
			}
			notes[index].note += directions[choice]
		case RotateRhythm:
			offset := op.Arg % len(notes)
			if offset < 0 {
				offset += len(notes)
			}
			if offset == 0 {
				continue
			}
			if locked != 0 {
				return nil, nil, fmt.Errorf("CICADA-LOCKED: rhythm rotation moves a locked step")
			}
			rotated := make([]noteState, len(notes))
			for index, note := range notes {
				rotated[(index+offset)%len(notes)] = note
			}
			notes = rotated
		case SwapSteps:
			eligible := editableOnsets(notes, locked)
			if len(eligible) < 2 {
				continue
			}
			first := chooseStep(&s, "mutation", eligible)
			for position, index := range eligible {
				if index == first {
					eligible = append(eligible[:position], eligible[position+1:]...)
					break
				}
			}
			second := chooseStep(&s, "mutation", eligible)
			notes[first], notes[second] = notes[second], notes[first]
		case FillRest:
			var eligible []int
			for index, note := range notes {
				if !note.active && locked&(uint64(1)<<index) == 0 {
					eligible = append(eligible, index)
				}
			}
			index := chooseStep(&s, "mutation", eligible)
			if index >= 0 {
				notes[index] = noteState{active: true, class: classRoot, note: root}
			}
		case Thin:
			var eligible []int
			for _, index := range editableOnsets(notes, locked) {
				if notes[index].class != classRoot {
					eligible = append(eligible, index)
				}
			}
			index := chooseStep(&s, "mutation", eligible)
			if index >= 0 {
				notes[index] = noteState{}
			}
		case Ratchet:
			var eligible []int
			for _, index := range editableOnsets(notes, locked) {
				if notes[index].accent {
					eligible = append(eligible, index)
				}
			}
			index := chooseStep(&s, "mutation", eligible)
			if index >= 0 {
				notes[index].ratchet = uint8(2 + s.choose("mutation", index, 2))
			}
		default:
			return nil, nil, fmt.Errorf("unknown phrase mutation %d", op.Kind)
		}
	}
	if err := repairBar(notes, root, p.RestDownbeat, locked); err != nil {
		return nil, nil, err
	}
	return notes, s.trace, nil
}

func editableOnsets(notes []noteState, locked uint64) []int {
	var eligible []int
	for index, note := range notes {
		if note.active && !note.tie && locked&(uint64(1)<<index) == 0 {
			eligible = append(eligible, index)
		}
	}
	return eligible
}

func chooseStep(s *stream, pass string, eligible []int) int {
	if len(eligible) == 0 {
		return -1
	}
	index := eligible[s.choose(pass, -1, len(eligible))]
	s.trace[len(s.trace)-1].Step = index
	return index
}

func octaveDirections(note int) []int {
	switch {
	case note < 36:
		if note+12 <= 127 {
			return []int{12}
		}
	case note > 72:
		if note-12 >= 0 {
			return []int{-12}
		}
	default:
		var directions []int
		if note-12 >= 36 {
			directions = append(directions, -12)
		}
		if note+12 <= 72 {
			directions = append(directions, 12)
		}
		return directions
	}
	return nil
}
