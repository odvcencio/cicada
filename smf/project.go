package smf

import (
	"fmt"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/project"
)

type heldNote struct {
	index int
	step  int64
	slide bool
	valid bool
}

type probabilityIdentity struct {
	track uint8
	slot  uint8
}

// FromProject compiles the first pass of a song into type 1 MIDI. The
// probability hash always uses iteration zero, as specified for interchange.
func FromProject(p *project.Project, bars int) (File, error) {
	return fromProject(p, bars, nil)
}

func fromProject(p *project.Project, bars int, identity *probabilityIdentity) (File, error) {
	var file File
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		return file, err
	}
	totalBars := 0
	for _, entry := range cfg.Song {
		totalBars += int(entry.Bars)
	}
	if bars == 0 {
		bars = totalBars
	}
	if bars < 1 || bars > totalBars || bars > 256 {
		return file, fmt.Errorf("MIDI export requires 1 to 256 song bars")
	}
	endTick := int64(bars) * seq.TicksPerBar
	file.Format, file.PPQ = 1, uint16(seq.PPQ)
	file.TempoMicros = uint32((60_000_000_000 + int64(p.TempoMilli)/2) / int64(p.TempoMilli))
	keySharps, keyMinor := keySignature(p.Key)
	file.Tracks = make([]TrackChunk, len(p.Tracks)+1)
	file.Tracks[0] = TrackChunk{
		Name: p.Title, EndTick: endTick,
		Meta: []MetaEvent{
			{Type: 0x51, Data: []byte{byte(file.TempoMicros >> 16), byte(file.TempoMicros >> 8), byte(file.TempoMicros)}},
			{Type: 0x58, Data: []byte{4, 2, 24, 8}},
			{Type: 0x59, Data: []byte{byte(keySharps), keyMinor}},
		},
	}
	channels := make([]uint8, len(p.Tracks))
	nextChannel := uint8(0)
	for i, track := range p.Tracks {
		if cfg.Track[i].Kind == engine.VoiceDrums {
			channels[i] = 9
		} else {
			if nextChannel == 9 {
				nextChannel++
			}
			if nextChannel >= 16 {
				return File{}, fmt.Errorf("MIDI has only 15 distinct melodic channels")
			}
			channels[i] = nextChannel
			nextChannel++
		}
		file.Tracks[i+1] = TrackChunk{Name: track.ID, EndTick: endTick}
	}
	var slots [16]int
	for i := range slots {
		slots[i] = -1
	}
	var held [16][drum.LaneCount]heldNote
	bar := 0
	for _, entry := range cfg.Song {
		scene := cfg.Scenes[entry.Scene]
		for repeat := 0; repeat < int(entry.Bars) && bar < bars; repeat++ {
			boundary := int64(bar) * seq.TicksPerBar
			for ti := range p.Tracks {
				binding := scene.Track[ti]
				switch binding.Mode {
				case engine.SceneOff:
					for lane := range held[ti] {
						closeAt(&file.Tracks[ti+1], &held[ti][lane], boundary)
					}
					slots[ti] = -1
				case engine.SceneSlot:
					slots[ti] = int(binding.Slot)
				}
			}
			for step := 0; step < 16; step++ {
				absoluteStep := int64(bar*16 + step)
				for ti := range p.Tracks {
					slot := slots[ti]
					if slot < 0 {
						continue
					}
					probabilityTrack, probabilitySlot := uint8(ti), uint8(slot)
					if identity != nil {
						probabilityTrack, probabilitySlot = identity.track, identity.slot
					}
					if cfg.Track[ti].Kind == engine.VoiceDrums {
						for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
							pattern := &cfg.Patterns[ti].Drums[slot][lane]
							if pattern.Len != 0 {
								appendStep(&file.Tracks[ti+1], pattern, absoluteStep, uint8(ti), probabilityTrack, probabilitySlot, drum.MIDINotes[lane], true, channels[ti], &held[ti][lane])
							}
						}
					} else {
						pattern := &cfg.Patterns[ti].Slots[slot]
						if pattern.Len != 0 {
							appendStep(&file.Tracks[ti+1], pattern, absoluteStep, uint8(ti), probabilityTrack, probabilitySlot, 0, false, channels[ti], &held[ti][0])
						}
					}
				}
			}
			bar++
		}
		if bar >= bars {
			break
		}
	}
	return file, nil
}

// FromPattern exports a selected pattern with its first compatible track.
// A short pattern loops to fill one bar; longer patterns retain their length.
func FromPattern(p *project.Project, patternID string) (File, error) {
	if err := project.ValidateProject(p); err != nil {
		return File{}, err
	}
	var pattern *project.Pattern
	for i := range p.Patterns {
		if p.Patterns[i].ID == patternID {
			pattern = &p.Patterns[i]
			break
		}
	}
	if pattern == nil {
		return File{}, fmt.Errorf("unknown pattern %s", patternID)
	}
	selected := -1
	selectedSlot := 0
	for i, track := range p.Tracks {
		for slotIndex, slot := range track.Slots {
			if slot != nil && *slot == patternID {
				selected = i
				selectedSlot = slotIndex
				break
			}
		}
		if selected >= 0 {
			break
		}
	}
	if selected < 0 {
		for i, track := range p.Tracks {
			isDrum := track.Kind == "drums"
			for _, kit := range p.Kits {
				isDrum = isDrum || kit.ID == track.Kind
			}
			if pattern.Kind == "drums" && isDrum || pattern.Kind == "acid" && track.Kind == "acid" || pattern.Kind == "notes" && !isDrum {
				selected = i
				break
			}
		}
	}
	if selected < 0 {
		return File{}, fmt.Errorf("pattern %s has no compatible track", patternID)
	}
	clone := *p
	track := p.Tracks[selected]
	track.Slots = [16]*string{}
	track.Slots[0] = &patternID
	clone.Tracks = []project.Track{track}
	clone.Scenes = []project.Scene{{ID: "midi-pattern", Bindings: map[string]string{track.ID: patternID}}}
	bars := int(pattern.Steps+15) / 16
	clone.Song = []project.SongEntry{{Scene: "midi-pattern", Bars: uint16(bars)}}
	clone.Title = patternID
	return fromProject(&clone, bars, &probabilityIdentity{track: uint8(selected), slot: uint8(selectedSlot)})
}

func appendStep(track *TrackChunk, pattern *seq.Pattern, absoluteStep int64, trackIndex, probabilityTrack, probabilitySlot, drumPitch uint8, isDrum bool, channel uint8, held *heldNote) {
	index := uint8(absoluteStep % int64(pattern.Len))
	step, err := seq.UnpackStep(pattern.Steps[index])
	if err != nil || !step.Gate || !seq.ProbabilityHit(step.Probability, pattern.Seed, probabilityTrack, probabilitySlot, 0, index) {
		return
	}
	start := absoluteStep * seq.TicksPerStep
	end := (absoluteStep + 1) * seq.TicksPerStep
	if absoluteStep&1 != 0 {
		start += seq.SwingDelayTicks(pattern.SwingPermille)
	} else {
		end += seq.SwingDelayTicks(pattern.SwingPermille)
	}
	if step.Tie {
		if held.valid && held.step == absoluteStep-1 {
			note := &track.Notes[held.index]
			note.Dur = max(note.Dur, end-note.Tick)
			held.step, held.slide = absoluteStep, false
		}
		return
	}
	for ratchet := uint8(0); ratchet < step.Ratchet; ratchet++ {
		onset := seq.RatchetTick(start, end, step.Ratchet, ratchet)
		segmentEnd := end
		if ratchet+1 < step.Ratchet {
			segmentEnd = seq.RatchetTick(start, end, step.Ratchet, ratchet+1)
		}
		if ratchet == 0 && held.valid && held.step == absoluteStep-1 && held.slide && !isDrum {
			previous := &track.Notes[held.index]
			previous.Dur = max(int64(1), onset+1-previous.Tick)
		}
		gate := max(int64(30), (segmentEnd-onset)*int64(pattern.GatePercent)/100)
		pitch := uint8(int(step.Note) + int(pattern.Transpose))
		velocity := uint8(100)
		if isDrum {
			pitch, velocity = drumPitch, step.Velocity
		} else if step.Accent {
			velocity = 127
		}
		track.Notes = append(track.Notes, Note{
			Tick: onset, Dur: gate, Note: pitch, Vel: velocity,
			Chan: channel, Track: trackIndex + 1,
		})
		held.index, held.step, held.slide, held.valid = len(track.Notes)-1, absoluteStep, step.Slide && !isDrum && ratchet+1 == step.Ratchet, true
	}
}

func closeAt(track *TrackChunk, held *heldNote, tick int64) {
	if held.valid {
		note := &track.Notes[held.index]
		if note.Tick+note.Dur > tick {
			note.Dur = max(int64(1), tick-note.Tick)
		}
	}
	held.valid = false
}

func keySignature(key project.Key) (int8, uint8) {
	root := int(key.Root)
	minor := uint8(0)
	switch key.Scale {
	case "minor", "harmonic", "pent", "blues":
		root = (root + 3) % 12
		minor = 1
	case "dorian":
		root = (root + 10) % 12
	case "phrygian":
		root = (root + 8) % 12
	case "mixo":
		root = (root + 5) % 12
	}
	return [12]int8{0, -5, 2, -3, 4, -1, 6, 1, -4, 3, -2, 5}[root], minor
}
