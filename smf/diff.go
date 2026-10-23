package smf

import (
	"fmt"
	"slices"
	"strings"
)

type musicalNote struct {
	Tick, Dur int64
	Pitch     uint8
	Velocity  uint8
	Channel   uint8
	Port      string
}

type tempoPoint struct {
	Tick   int64
	Micros uint32
}

// MusicalDiff compares tempo, notes, routing, and ordered channel messages,
// ignoring track layout and legal wire encodings of the same events.
func MusicalDiff(a, b File) error {
	var err error
	if a, err = prepareGestureMIDI(a); err != nil {
		return err
	}
	if b, err = prepareGestureMIDI(b); err != nil {
		return err
	}
	if a.PPQ != b.PPQ {
		return fmt.Errorf("PPQ differs: %d versus %d", a.PPQ, b.PPQ)
	}
	leftTempo, rightTempo := tempos(a), tempos(b)
	if !slices.Equal(leftTempo, rightTempo) {
		return fmt.Errorf("tempo events differ: %v versus %v", leftTempo, rightTempo)
	}
	leftNotes, rightNotes := normalizedNotes(a), normalizedNotes(b)
	if len(leftNotes) != len(rightNotes) {
		return fmt.Errorf("note counts differ: %d versus %d", len(leftNotes), len(rightNotes))
	}
	for i := range leftNotes {
		if leftNotes[i] != rightNotes[i] {
			return fmt.Errorf("note event %d differs: %+v versus %+v", i, leftNotes[i], rightNotes[i])
		}
	}
	if !slices.Equal(normalizedControllers(a), normalizedControllers(b)) {
		return fmt.Errorf("controller events or routing differ")
	}
	return nil
}

func normalizedNotes(file File) []musicalNote {
	var notes []musicalNote
	for _, track := range file.Tracks {
		for _, note := range track.Notes {
			notes = append(notes, musicalNote{note.Tick, note.Dur, note.Note, note.Vel, note.Chan, midiPortAt(track, note.Tick)})
		}
	}
	slices.SortFunc(notes, func(a, b musicalNote) int {
		if cmp := strings.Compare(a.Port, b.Port); cmp != 0 {
			return cmp
		}
		for _, cmp := range []int{
			cmp64(a.Tick, b.Tick), int(a.Channel) - int(b.Channel),
			int(a.Pitch) - int(b.Pitch), cmp64(a.Dur, b.Dur), int(a.Velocity) - int(b.Velocity),
		} {
			if cmp != 0 {
				return cmp
			}
		}
		return 0
	})
	return notes
}

type musicalController struct {
	Port string
	ChannelEvent
}

func normalizedControllers(file File) []musicalController {
	var result []musicalController
	for _, track := range file.Tracks {
		for _, event := range track.Events {
			result = append(result, musicalController{midiPortAt(track, event.Tick), event})
		}
	}
	// Preserve ordering within each port/channel/tick, including RPN setup.
	slices.SortStableFunc(result, func(a, b musicalController) int {
		if cmp := strings.Compare(a.Port, b.Port); cmp != 0 {
			return cmp
		}
		if cmp := int(a.Status&15) - int(b.Status&15); cmp != 0 {
			return cmp
		}
		return cmp64(a.Tick, b.Tick)
	})
	return result
}

func midiPortAt(track TrackChunk, tick int64) string {
	name, number := "", ""
	nameTick, numberTick := int64(-1), int64(-1)
	for _, meta := range track.Meta {
		if meta.Tick > tick {
			continue
		}
		if meta.Type == 0x09 && meta.Tick >= nameTick {
			name, nameTick = string(meta.Data), meta.Tick
		}
		if meta.Type == 0x21 && meta.Tick >= numberTick {
			number, numberTick = string(meta.Data), meta.Tick
		}
	}
	return name + "\x00" + number
}

func tempos(file File) []tempoPoint {
	var points []tempoPoint
	for _, track := range file.Tracks {
		for _, meta := range track.Meta {
			if meta.Type == 0x51 && len(meta.Data) == 3 {
				points = append(points, tempoPoint{meta.Tick, uint32(meta.Data[0])<<16 | uint32(meta.Data[1])<<8 | uint32(meta.Data[2])})
			}
		}
	}
	if len(points) == 0 && file.TempoMicros != 0 {
		points = append(points, tempoPoint{0, file.TempoMicros})
	}
	slices.SortFunc(points, func(a, b tempoPoint) int {
		if a.Tick != b.Tick {
			return cmp64(a.Tick, b.Tick)
		}
		return int(a.Micros) - int(b.Micros)
	})
	return points
}

func cmp64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
