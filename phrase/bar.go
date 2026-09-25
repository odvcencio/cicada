package phrase

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
)

func buildBaseBar(p normalizedParams) ([]noteState, []Draw, error) {
	s := newStream(p.Seed)
	onsets := generateRhythm(&s, int(p.Steps), p.density, p.RestDownbeat)
	notes, err := generateDegrees(&s, onsets, p)
	if err != nil {
		return nil, nil, err
	}
	jumpOctaves(&s, notes, p.octaveJump)
	addAccents(notes, p.accentDensity)
	addSlides(&s, notes, p.slideDensity)
	if err := repairBar(notes, 12*(int(p.RootOctave)+1)+int(p.Key), p.RestDownbeat, 0); err != nil {
		return nil, nil, err
	}
	return notes, s.trace, nil
}

func repairBar(notes []noteState, root int, restDownbeat bool, locked uint64) error {
	if len(notes) == 0 {
		return fmt.Errorf("cannot repair an empty phrase")
	}
	for index := range notes {
		next := (index + 1) % len(notes)
		if notes[index].slide && !notes[next].active {
			if locked&(uint64(1)<<index) != 0 {
				return fmt.Errorf("CICADA-LOCKED: slide repair needs step %d", index)
			}
			notes[index].slide = false
		}
		if notes[index].tie && (!notes[index].active || !notes[(index+len(notes)-1)%len(notes)].active) {
			if locked&(uint64(1)<<index) != 0 {
				return fmt.Errorf("CICADA-LOCKED: tie repair needs step %d", index)
			}
			notes[index].tie = false
		}
	}
	if !restDownbeat {
		if locked&1 != 0 && (!notes[0].active || notes[0].class != classRoot || notes[0].tie) {
			return fmt.Errorf("CICADA-LOCKED: root downbeat needs step 0")
		}
		notes[0].active, notes[0].class, notes[0].tie = true, classRoot, false
		notes[0].note = nearestOctave(root, notes[0].note)
	}
	rootExists := false
	for _, note := range notes {
		rootExists = rootExists || note.active && note.class == classRoot && !note.tie
	}
	if !rootExists {
		best := -1
		for index, note := range notes {
			if !note.active || best >= 0 && metricStrength[index%metricPeriod] <= metricStrength[best%metricPeriod] {
				continue
			}
			best = index
		}
		if best < 0 || locked&(uint64(1)<<best) != 0 {
			return fmt.Errorf("CICADA-LOCKED: root repair has no editable onset")
		}
		notes[best].class, notes[best].tie = classRoot, false
		notes[best].note = nearestOctave(root, notes[best].note)
	}
	previous := -1
	for index, note := range notes {
		if !note.active || note.tie {
			continue
		}
		if previous >= 0 {
			for abs(notes[index].note-notes[previous].note) > 12 {
				if locked&(uint64(1)<<index) != 0 {
					return fmt.Errorf("CICADA-LOCKED: interval repair needs step %d", index)
				}
				if notes[index].note > notes[previous].note {
					notes[index].note -= 12
				} else {
					notes[index].note += 12
				}
				if notes[index].note < 0 || notes[index].note > 127 {
					return fmt.Errorf("interval repair moved a pitch outside MIDI range")
				}
			}
		}
		previous = index
	}
	return nil
}

func nearestOctave(root, target int) int {
	best := root
	for note := root - 48; note <= root+48; note += 12 {
		if note < 0 || note > 127 {
			continue
		}
		if abs(note-target) < abs(best-target) {
			best = note
		}
	}
	return best
}

func encodeBar(notes []noteState, p normalizedParams) (seq.Pattern, error) {
	swing, err := seq.SwingFromPercent100(p.SwingPercent100)
	if err != nil {
		return seq.Pattern{}, err
	}
	pattern := seq.Pattern{
		Len: uint8(len(notes)), SwingPermille: swing,
		GatePercent: p.GatePercent, Seed: uint32(p.Seed),
	}
	for index, note := range notes {
		step := seq.Step{Ratchet: 1, Probability: 100, Velocity: 100}
		if note.active {
			step.Gate = true
			if note.tie {
				step.Tie = true
			} else {
				step.Note = uint8(note.note)
				step.Accent = note.accent
				step.Slide = note.slide
				if note.ratchet > 1 {
					step.Ratchet = note.ratchet
				}
			}
		}
		pattern.Steps[index], err = seq.PackStep(step)
		if err != nil {
			return seq.Pattern{}, err
		}
	}
	return pattern, pattern.Validate()
}
