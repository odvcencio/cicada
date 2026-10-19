package smf

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/cicada/kernel/seq"
)

const maxGestureMIDIEvents = 1 << 20

type midiGestureSample struct {
	tick                    int64
	pitch, pressure, timbre float64
}
type midiTempo struct {
	tick    int64
	seconds float64
	micros  uint32
}

func midiTempoMap(file File) ([]midiTempo, error) {
	initial := file.TempoMicros
	if initial == 0 {
		initial = 500000
	}
	result := []midiTempo{{micros: initial}}
	for _, point := range tempos(file) {
		if point.Tick < 0 || point.Micros == 0 {
			return nil, fmt.Errorf("invalid gesture MIDI tempo")
		}
		last := &result[len(result)-1]
		if last.tick == point.Tick {
			last.micros = point.Micros
			continue
		}
		seconds := last.seconds + float64(point.Tick-last.tick)*float64(last.micros)/(float64(file.PPQ)*1e6)
		result = append(result, midiTempo{tick: point.Tick, seconds: seconds, micros: point.Micros})
	}
	return result, nil
}

func midiSeconds(tempo []midiTempo, ppq uint16, tick int64) float64 {
	i := sort.Search(len(tempo), func(i int) bool { return tempo[i].tick > tick }) - 1
	if i < 0 {
		i = 0
	}
	p := tempo[i]
	return p.seconds + float64(tick-p.tick)*float64(p.micros)/(float64(ppq)*1e6)
}

func midiTick(tempo []midiTempo, ppq uint16, seconds float64) int64 {
	i := sort.Search(len(tempo), func(i int) bool { return tempo[i].seconds > seconds }) - 1
	if i < 0 {
		i = 0
	}
	p := tempo[i]
	return p.tick + int64(math.Round((seconds-p.seconds)*float64(ppq)*1e6/float64(p.micros)))
}

func midiGestureSamples(note Note, tempo []midiTempo, ppq uint16, budget *int) ([]midiGestureSample, int, error) {
	points := []seq.PerformedExpression{{Params: seq.Expression{Set: true, Timbre: .5}}}
	if note.Gesture != nil {
		previous := int64(-1)
		for _, point := range note.Gesture.Points {
			if point.Tick <= previous || point.Tick < 0 || point.Tick > note.Dur || !point.Params.Set {
				return nil, 0, fmt.Errorf("invalid MIDI gesture positions")
			}
			if err := point.Params.Validate(); err != nil {
				return nil, 0, err
			}
			previous = point.Tick
			if point.Tick == note.Dur && note.Gesture.Group == 0 {
				continue
			}
			if point.Tick == 0 {
				points[0] = point
			} else {
				points = append(points, point)
			}
		}
	}
	var result []midiGestureSample
	phase, peak := float64(0), float64(0)
	lastTime := midiSeconds(tempo, ppq, note.Tick)
	active := seq.Expression{Set: true, Timbre: .5}
	advance := func(at int64) {
		now := midiSeconds(tempo, ppq, note.Tick+at)
		if active.VibratoDepthCents != 0 && active.VibratoRateHz != 0 {
			phase += (now - lastTime) * float64(active.VibratoRateHz)
			phase -= math.Floor(phase)
		}
		lastTime = now
	}
	emit := func(at int64) error {
		if *budget == 0 {
			return fmt.Errorf("MIDI gesture export exceeds one million controller samples")
		}
		*budget--
		cents := float64(active.PitchCents)
		if active.VibratoDepthCents != 0 && active.VibratoRateHz != 0 {
			cents += float64(active.VibratoDepthCents) * math.Sin(2*math.Pi*phase)
		}
		peak = max(peak, math.Abs(cents))
		result = append(result, midiGestureSample{tick: note.Tick + at, pitch: cents, pressure: float64(active.Pressure), timbre: float64(active.Timbre)})
		return nil
	}
	for i, point := range points {
		advance(point.Tick)
		active = point.Params
		// Choose a range for the entire vibrato envelope, including peaks
		// between sampled MIDI messages.
		peak = max(peak, math.Abs(float64(active.PitchCents))+float64(active.VibratoDepthCents))
		if err := emit(point.Tick); err != nil {
			return nil, 0, err
		}
		end := note.Dur
		if i+1 < len(points) {
			end = points[i+1].Tick
		}
		if active.VibratoDepthCents == 0 || active.VibratoRateHz == 0 {
			continue
		}
		step := 1 / max(240, float64(active.VibratoRateHz)*32)
		next := lastTime + step
		lastTick := point.Tick
		for {
			at := max(lastTick+1, midiTick(tempo, ppq, next)-note.Tick)
			if at >= end {
				break
			}
			advance(at)
			if err := emit(at); err != nil {
				return nil, 0, err
			}
			lastTick, next = at, next+step
		}
	}
	if peak > 9600 {
		return nil, 0, fmt.Errorf("gesture exceeds the 96-semitone MPE bend range; render WAV or reduce its bend/depth")
	}
	return result, max(2, int(math.Ceil(peak/100))), nil
}

// prepareGestureMIDI isolates expressive notes on MPE member channels. Logical
// ports and extra chunks preserve overlapping voices beyond one MPE zone.
// Existing note-only files keep their exact PPQ, channels and track layout.
func prepareGestureMIDI(file File) (File, error) {
	found := false
	for _, track := range file.Tracks {
		for _, note := range track.Notes {
			found = found || note.Gesture != nil
		}
	}
	if !found {
		return file, nil
	}
	if file.PPQ == 0 || file.PPQ > 32767 {
		return File{}, fmt.Errorf("invalid gesture MIDI PPQ")
	}
	factor := int64(32767 / int(file.PPQ))
	if len(file.Tracks) > 256 {
		return File{}, fmt.Errorf("MIDI gesture export exceeds 256 tracks")
	}
	// Validate before scaling so malformed public File values cannot wrap.
	limit := int64(1<<62) / factor
	for _, track := range file.Tracks {
		if track.EndTick < 0 || track.EndTick > limit {
			return File{}, fmt.Errorf("invalid gestural MIDI end tick")
		}
		for _, meta := range track.Meta {
			if meta.Tick < 0 || meta.Tick > limit {
				return File{}, fmt.Errorf("invalid gestural MIDI meta tick")
			}
		}
		for _, event := range track.Events {
			if event.Tick < 0 || event.Tick > limit {
				return File{}, fmt.Errorf("invalid gestural MIDI controller tick")
			}
		}
		for _, note := range track.Notes {
			if note.Tick < 0 || note.Dur < 1 || note.Dur > limit || note.Tick > limit-note.Dur || note.Note > 127 || note.Vel < 1 || note.Vel > 127 || note.Chan > 15 {
				return File{}, fmt.Errorf("invalid gestural MIDI note")
			}
			if note.Gesture != nil {
				if note.Gesture.Group < 0 || note.Gesture.Group > len(track.Groups) {
					return File{}, fmt.Errorf("MIDI note refers to an unknown shared gesture lane")
				}
				for _, point := range note.Gesture.Points {
					if point.Tick < 0 || point.Tick > note.Dur {
						return File{}, fmt.Errorf("invalid gestural MIDI controller tick")
					}
				}
			}
		}
		for _, group := range track.Groups {
			previous := int64(-1)
			if len(group.Points) == 0 {
				return File{}, fmt.Errorf("empty MIDI shared gesture lane")
			}
			for _, point := range group.Points {
				if point.Tick <= previous || point.Tick < 0 || point.Tick > track.EndTick || point.Tick > limit {
					return File{}, fmt.Errorf("invalid MIDI shared gesture position")
				}
				if err := point.Value.Validate(); err != nil {
					return File{}, err
				}
				previous = point.Tick
			}
		}
	}
	out := file
	out.PPQ = uint16(int64(file.PPQ) * factor)
	out.Tracks = make([]TrackChunk, len(file.Tracks))
	var usedPorts [256]bool
	usedNames := make(map[string]bool)
	usedPorts[0] = true
	for _, track := range file.Tracks {
		for _, meta := range track.Meta {
			if meta.Type == 0x21 && len(meta.Data) == 1 {
				usedPorts[meta.Data[0]] = true
			}
			if meta.Type == 0x09 {
				usedNames[string(meta.Data)] = true
			}
		}
	}
	port := func() (byte, string, error) {
		for i := 1; i < len(usedPorts); i++ {
			name := fmt.Sprintf("Cicada MPE %03d", i)
			if !usedPorts[i] && !usedNames[name] {
				usedPorts[i] = true
				usedNames[name] = true
				return byte(i), name, nil
			}
		}
		return 0, "", fmt.Errorf("MIDI gesture export exhausted logical ports")
	}
	tempoFile := file
	tempoFile.PPQ = out.PPQ
	tempoFile.Tracks = make([]TrackChunk, len(file.Tracks))
	for i, track := range file.Tracks {
		copy := track
		copy.Meta = append([]MetaEvent(nil), track.Meta...)
		for j := range copy.Meta {
			copy.Meta[j].Tick *= factor
		}
		tempoFile.Tracks[i] = copy
	}
	tempo, err := midiTempoMap(tempoFile)
	if err != nil {
		return File{}, err
	}
	budget := maxGestureMIDIEvents
	for ti, original := range file.Tracks {
		track := tempoFile.Tracks[ti]
		track.EndTick *= factor
		track.Events = append([]ChannelEvent(nil), original.Events...)
		for i := range track.Events {
			track.Events[i].Tick *= factor
		}
		expressive := false
		for _, note := range original.Notes {
			expressive = expressive || note.Gesture != nil
		}
		track.Notes = nil
		if !expressive {
			track.Notes = append([]Note(nil), original.Notes...)
			for i := range track.Notes {
				track.Notes[i].Tick *= factor
				track.Notes[i].Dur *= factor
			}
			out.Tracks[ti] = track
			continue
		}
		if len(original.Events) != 0 {
			return File{}, fmt.Errorf("cannot combine channel-wide MIDI events with note-owned gestures")
		}
		for _, meta := range original.Meta {
			if meta.Type == 0x09 || meta.Type == 0x21 {
				return File{}, fmt.Errorf("cannot reroute a MIDI track with existing device/port metadata and note-owned gestures")
			}
		}
		type group struct {
			track TrackChunk
			until [16]int64
			index int
			lane  int
		}
		var groups []group
		newGroup := func(lane int) error {
			p, name, err := port()
			if err != nil {
				return err
			}
			g := group{track: TrackChunk{Name: track.Name, EndTick: track.EndTick}, index: ti, lane: lane}
			if len(groups) == 0 {
				g.track.Meta = append([]MetaEvent(nil), track.Meta...)
			} else {
				g.index = len(out.Tracks)
				out.Tracks = append(out.Tracks, TrackChunk{})
				g.track.Name = fmt.Sprintf("%s · MPE %d", track.Name, len(groups)+1)
			}
			if g.index > 255 {
				return fmt.Errorf("MIDI gesture export exceeds 256 tracks")
			}
			// RP-019 device names are the standard routing hint. Also emit the
			// widely supported, nonstandard numeric port event for older DAWs.
			g.track.Meta = append(g.track.Meta, MetaEvent{Type: 0x09, Data: []byte(name)}, MetaEvent{Type: 0x21, Data: []byte{p}})
			for _, pair := range [][2]byte{{101, 0}, {100, 6}, {6, 15}, {101, 127}, {100, 127}} {
				g.track.Events = append(g.track.Events, ChannelEvent{Status: 0xb0, Data1: pair[0], Data2: pair[1]})
			}
			if lane != 0 {
				shared := &original.Groups[lane-1]
				peak := float64(0)
				for _, point := range shared.Points {
					peak = max(peak, math.Abs(float64(point.Value.PitchCents)))
				}
				sensitivity := max(2, int(math.Ceil(peak/100)))
				for _, pair := range [][2]byte{{101, 0}, {100, 0}, {6, byte(sensitivity)}, {38, 0}, {101, 127}, {100, 127}} {
					g.track.Events = append(g.track.Events, ChannelEvent{Status: 0xb0, Data1: pair[0], Data2: pair[1]})
				}
				for _, point := range shared.Points {
					if budget == 0 {
						return fmt.Errorf("MIDI gesture export exceeds one million controller samples")
					}
					budget--
					bend := max(0, min(16383, 8192+int(math.Round(float64(point.Value.PitchCents)/float64(sensitivity*100)*8192))))
					at := point.Tick * factor
					g.track.Events = append(g.track.Events,
						ChannelEvent{Tick: at, Status: 0xe0, Data1: byte(bend & 127), Data2: byte(bend >> 7)},
						ChannelEvent{Tick: at, Status: 0xd0, Data1: byte(math.Round(float64(point.Value.Pressure) * 127))},
						ChannelEvent{Tick: at, Status: 0xb0, Data1: 74, Data2: byte(math.Round(float64(point.Value.Timbre) * 127))})
				}
			}
			groups = append(groups, g)
			return nil
		}
		notes := append([]Note(nil), original.Notes...)
		sort.SliceStable(notes, func(i, j int) bool { return notes[i].Tick < notes[j].Tick })
		for _, note := range notes {
			lane := 0
			if note.Gesture != nil {
				lane = note.Gesture.Group
			}
			if lane != 0 {
				if err := validateSharedMIDIRange(note, original.Groups[lane-1]); err != nil {
					return File{}, err
				}
			}
			note.Tick *= factor
			note.Dur *= factor
			if note.Gesture != nil {
				points := append([]seq.PerformedExpression(nil), note.Gesture.Points...)
				for i := range points {
					points[i].Tick *= factor
				}
				note.Gesture = &NoteGesture{Points: points, Group: lane}
			}
			gi, ch := -1, 0
			for i := range groups {
				if groups[i].lane != lane {
					continue
				}
				for c := 1; c < 16; c++ {
					if groups[i].until[c] <= note.Tick {
						gi, ch = i, c
						break
					}
				}
				if gi >= 0 {
					break
				}
			}
			if gi < 0 {
				if err := newGroup(lane); err != nil {
					return File{}, err
				}
				gi, ch = len(groups)-1, 1
			}
			g := &groups[gi]
			g.until[ch] = note.Tick + note.Dur
			samples, sensitivity, err := midiGestureSamples(note, tempo, out.PPQ, &budget)
			if err != nil {
				return File{}, err
			}
			for _, pair := range [][2]byte{{101, 0}, {100, 0}, {6, byte(sensitivity)}, {38, 0}, {101, 127}, {100, 127}} {
				g.track.Events = append(g.track.Events, ChannelEvent{Tick: note.Tick, Status: 0xb0 | byte(ch), Data1: pair[0], Data2: pair[1]})
			}
			lastBend, lastPressure, lastTimbre := -1, -1, -1
			for _, sample := range samples {
				bend := 8192 + int(math.Round(sample.pitch/float64(sensitivity*100)*8192))
				bend = max(0, min(16383, bend))
				pressure, timbre := int(math.Round(sample.pressure*127)), int(math.Round(sample.timbre*127))
				if bend != lastBend {
					g.track.Events = append(g.track.Events, ChannelEvent{Tick: sample.tick, Status: 0xe0 | byte(ch), Data1: byte(bend & 127), Data2: byte(bend >> 7), BeforeRelease: sample.tick == note.Tick+note.Dur})
				}
				if pressure != lastPressure {
					g.track.Events = append(g.track.Events, ChannelEvent{Tick: sample.tick, Status: 0xd0 | byte(ch), Data1: byte(pressure), BeforeRelease: sample.tick == note.Tick+note.Dur})
				}
				if timbre != lastTimbre {
					g.track.Events = append(g.track.Events, ChannelEvent{Tick: sample.tick, Status: 0xb0 | byte(ch), Data1: 74, Data2: byte(timbre), BeforeRelease: sample.tick == note.Tick+note.Dur})
				}
				lastBend, lastPressure, lastTimbre = bend, pressure, timbre
			}
			note.Chan, note.Track, note.Gesture = byte(ch), byte(g.index), nil
			g.track.Notes = append(g.track.Notes, note)
		}
		for _, g := range groups {
			out.Tracks[g.index] = g.track
		}
	}
	return out, nil
}
