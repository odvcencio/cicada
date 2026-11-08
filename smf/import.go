package smf

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type ImportOptions struct {
	// Zero infers an exact onset grid. An explicit grid permits quantization.
	GridTicks uint16
}

type ImportReport struct {
	Notes               int    `json:"notes"`
	Tracks              int    `json:"tracks"`
	Bars                int    `json:"bars"`
	GridTicks           uint16 `json:"grid_ticks"`
	QuantizedOnsets     int    `json:"quantized_onsets"`
	QuantizedDurations  int    `json:"quantized_durations"`
	QuantizedVelocities int    `json:"quantized_velocities"`
	SplitNotes          int    `json:"split_notes"`
	// MergedHits counts drum hits that landed in a step another hit of the same
	// drum already had; the louder one plays.
	MergedHits int `json:"merged_hits"`
}

// importLane is one score track: a melodic voice or the drums of one channel
// of one MIDI track. Notes that overlap get more than one melodic lane.
type importLane struct {
	source  int    // index of the MIDI track the notes came from
	channel uint8  // MIDI channel of the notes
	drums   bool   // channel-10 notes, a row per drum lane
	drum    string // in split mode, the one drum lane this track plays
	events  []importEvent
}

// importEvent is one step a lane plays: a note, a chord of two to four
// pitches that share an onset and a length, or a drum hit. vels holds each
// pitch's own velocity; the step plays the loudest.
type importEvent struct {
	tick, dur int64
	pitches   []uint8
	vels      []uint8
	vel       uint8
	drum      string
}

// errTooManyPatterns marks a lane that needs more than 16 bar patterns.
var errTooManyPatterns = errors.New("MIDI import: track needs more than 16 distinct bar patterns")

// patternLimitError is errTooManyPatterns for the lanes of one MIDI track.
type patternLimitError struct{ source int }

func (e patternLimitError) Error() string        { return errTooManyPatterns.Error() }
func (e patternLimitError) Is(target error) bool { return target == errTooManyPatterns }

type sourcedNote struct {
	Note
	source int
}

// ImportSource imports fixed-tempo 4/4 SMF notes to a validated edition-2
// score. Each MIDI track keeps its own score tracks, named after it: notes that
// start together with one length become a chord, other overlapping notes get
// another piano track, and drum notes share one drums track. Notes that cross
// a bar are split and retriggered; the report counts every timing edit.
func ImportSource(file File, options ImportOptions) ([]byte, ImportReport, error) {
	var report ImportReport
	fail := func(message string) ([]byte, ImportReport, error) {
		return nil, report, fmt.Errorf("MIDI import: %s", message)
	}
	if file.PPQ == 0 || file.PPQ > 32767 || file.Format > 1 {
		return fail("requires type 0 or 1 with PPQ timing")
	}
	tempo := file.TempoMicros
	if tempo == 0 {
		tempo = 500000
	}
	bpm := int64(60000000000) / int64(tempo)
	if bpm < 20000 || bpm > 300000 {
		return fail("tempo must be 20..300 BPM")
	}
	var notes []sourcedNote
	end := int64(0)
	grid := int64(960)
	for ti, track := range file.Tracks {
		if len(track.Events) != 0 {
			return fail("controller and program messages cannot yet be imported without loss; keep the source MIDI or render audio")
		}
		if track.EndTick < 0 || track.EndTick > int64(file.PPQ)*4*256 {
			return fail("song exceeds 256 bars")
		}
		end = max(end, (track.EndTick*seq.PPQ+int64(file.PPQ)-1)/int64(file.PPQ))
		for _, meta := range track.Meta {
			if meta.Type == 0x51 && meta.Tick != 0 {
				return fail("tempo changes are unsupported; use a fixed-tempo MIDI file")
			}
			if meta.Type == 0x58 && (len(meta.Data) != 4 || meta.Data[0] != 4 || meta.Data[1] != 2) {
				return fail("only 4/4 meter is supported")
			}
		}
		for _, original := range track.Notes {
			if original.Gesture != nil {
				return fail("note-owned gestures cannot yet be imported without loss; keep the source MIDI or render audio")
			}
			if original.Note < 12 || original.Note > 95 || original.Chan > 15 || original.Vel < 1 || original.Vel > 127 || original.Tick < 0 || original.Dur < 1 || original.Tick > 1<<40 || original.Dur > 1<<40 {
				return fail("note pitch, velocity or interval is outside score limits")
			}
			note := original
			startNumerator := note.Tick * seq.PPQ
			endNumerator := (note.Tick + note.Dur) * seq.PPQ
			if options.GridTicks == 0 && startNumerator%int64(file.PPQ) != 0 {
				return fail("onsets do not fit 960 PPQ; select --quantize explicitly")
			}
			note.Tick = (startNumerator + int64(file.PPQ)/2) / int64(file.PPQ)
			note.Dur = max(int64(1), (endNumerator+int64(file.PPQ)/2)/int64(file.PPQ)-note.Tick)
			if startNumerator%int64(file.PPQ) != 0 {
				report.QuantizedOnsets++
			}
			if endNumerator%int64(file.PPQ) != 0 {
				report.QuantizedDurations++
			}
			a, b := grid, note.Tick
			for b != 0 {
				a, b = b, a%b
			}
			grid = a
			end = max(end, note.Tick+note.Dur)
			notes = append(notes, sourcedNote{Note: note, source: ti})
		}
	}
	if len(notes) == 0 {
		return fail("file contains no notes")
	}
	if options.GridTicks != 0 {
		grid = int64(options.GridTicks)
	}
	if grid < 60 || grid > 960 || seq.TicksPerBar%grid != 0 {
		return fail("import needs an exact 60..960 tick grid dividing a bar; select --quantize 1/16 or 1/8t")
	}
	report.GridTicks = uint16(grid)
	report.Notes = len(notes)
	for i := range notes {
		old := notes[i].Tick
		notes[i].Tick = ((old + grid/2) / grid) * grid
		if old != notes[i].Tick {
			report.QuantizedOnsets++
		}
		end = max(end, notes[i].Tick+notes[i].Dur)
	}
	report.Bars = int((end + seq.TicksPerBar - 1) / seq.TicksPerBar)
	if report.Bars > 256 {
		return fail("song exceeds 256 bars")
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].Tick != notes[j].Tick {
			return notes[i].Tick < notes[j].Tick
		}
		return notes[i].Note.Note < notes[j].Note.Note
	})
	// A drum kit on one track and chords read best, but they can need more
	// than 16 distinct bar patterns where single drums and single notes do
	// not; then the import splits them, as earlier imports did.
	var source []byte
	var attempt ImportReport
	var err error
	split := map[int]bool{} // MIDI tracks whose kit and chords are split
	for attempts := 0; attempts <= len(file.Tracks); attempts++ {
		var lanes []importLane
		lanes, err = allocateLanes(notes, split)
		if err != nil {
			return nil, report, err
		}
		// Tracks follow the MIDI file's track order and then channel order; a
		// track's lanes keep the order they were opened in.
		sort.SliceStable(lanes, func(i, j int) bool {
			if lanes[i].source != lanes[j].source {
				return lanes[i].source < lanes[j].source
			}
			return lanes[i].channel < lanes[j].channel
		})
		attempt = report
		attempt.Tracks = len(lanes)
		for _, lane := range lanes {
			for _, e := range lane.events {
				for _, v := range e.vels {
					if v != e.vel {
						attempt.QuantizedVelocities++
					}
				}
			}
		}
		counted := attempt
		source, err = writeImport(file, lanes, importTrackNames(file, lanes), bpm, grid, &attempt)
		if err == nil {
			break
		}
		attempt = counted
		var limit patternLimitError
		if !errors.As(err, &limit) || split[limit.source] {
			return nil, report, err
		}
		split[limit.source] = true
	}
	if err != nil {
		return nil, report, err
	}
	return source, attempt, nil
}

// allocateLanes puts notes on score tracks. Each lane holds notes of one MIDI
// track and channel. Normally the drums of a channel share one lane with a row
// per drum, and notes that start together with one length form a chord; a
// MIDI track in split gives every drum its own lane and makes no chords.
func allocateLanes(notes []sourcedNote, splitSources map[int]bool) ([]importLane, error) {
	fail := func(message string) ([]importLane, error) {
		return nil, fmt.Errorf("MIDI import: %s", message)
	}
	var lanes []importLane
	open := func(lane importLane) (int, error) {
		if len(lanes) == limits.MaxTracks {
			return -1, fmt.Errorf("MIDI import: polyphony needs more than %d score tracks", limits.MaxTracks)
		}
		lanes = append(lanes, lane)
		return len(lanes) - 1, nil
	}
	for _, note := range notes {
		split := splitSources[note.source]
		if note.Chan == 9 {
			name := ""
			for i, pitch := range drum.MIDINotes {
				if pitch == note.Note.Note {
					name = drum.Names[i]
					break
				}
			}
			if name == "" {
				return fail("drum note has no built-in lane")
			}
			selected := -1
			for i := range lanes {
				if lanes[i].source == note.source && lanes[i].drums && (!split || lanes[i].drum == name) {
					selected = i
					break
				}
			}
			if selected < 0 {
				lane := importLane{source: note.source, channel: note.Chan, drums: true}
				if split {
					lane.drum = name
				}
				var err error
				if selected, err = open(lane); err != nil {
					return nil, err
				}
			}
			lanes[selected].events = append(lanes[selected].events, importEvent{tick: note.Tick, dur: note.Dur, vel: note.Vel, vels: []uint8{note.Vel}, drum: name})
			continue
		}
		// A note that starts with the last chord of a lane and lasts as long
		// joins it; otherwise it takes the first free lane of its track and
		// channel.
		chord, free := -1, -1
		for i := range lanes {
			lane := &lanes[i]
			if lane.source != note.source || lane.channel != note.Chan || lane.drums {
				continue
			}
			last := &lane.events[len(lane.events)-1]
			if !split && last.tick == note.Tick && last.dur == note.Dur && len(last.pitches) < 4 && !slices.Contains(last.pitches, note.Note.Note) {
				chord = i
				break
			}
			if free < 0 && last.tick+last.dur <= note.Tick {
				free = i
			}
		}
		if chord >= 0 {
			last := &lanes[chord].events[len(lanes[chord].events)-1]
			last.pitches = append(last.pitches, note.Note.Note)
			last.vels = append(last.vels, note.Vel)
			last.vel = max(last.vel, note.Vel)
			continue
		}
		if free < 0 {
			var err error
			if free, err = open(importLane{source: note.source, channel: note.Chan}); err != nil {
				return nil, err
			}
		}
		lanes[free].events = append(lanes[free].events, importEvent{tick: note.Tick, dur: note.Dur, pitches: []uint8{note.Note.Note}, vels: []uint8{note.Vel}, vel: note.Vel})
	}
	return lanes, nil
}

// writeImport spells the lanes as a formatted, validated score. names holds
// one track ID per lane; report gathers the timing edits it makes.
func writeImport(file File, lanes []importLane, names []string, bpm, grid int64, report *ImportReport) ([]byte, error) {
	fail := func(message string) ([]byte, error) {
		return nil, fmt.Errorf("MIDI import: %s", message)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "cicada 2\ntitle %q\ntempo %d.%03d\n", importTitle(file), bpm/1000, bpm%1000)
	bindings := make([][]string, report.Bars)
	for ti, lane := range lanes {
		trackID := names[ti]
		kind := "piano"
		if lane.drums {
			kind = "drums"
		}
		fmt.Fprintf(&output, "track %s %s {}\n", trackID, kind)
		gate := int64(100)
		for _, e := range lane.events {
			if e.dur < grid {
				gate = max(int64(10), min(int64(100), (e.dur*100+grid/2)/grid))
				break
			}
		}
		pool := map[string]string{}
		for bar := 0; bar < report.Bars; bar++ {
			cells := make([]string, int(seq.TicksPerBar/grid))
			velocities := make([]string, len(cells))
			for i := range cells {
				cells[i] = "."
				velocities[i] = "100"
			}
			rows := map[string][]string{}
			start := int64(bar) * seq.TicksPerBar
			// Two hits of one drum in one step keep the louder. Choose those
			// first, so the report counts edits only for hits that play.
			winner := map[string]int{} // drum and cell -> event index
			if lane.drums {
				for i, e := range lane.events {
					if e.tick >= start+seq.TicksPerBar || e.tick+e.dur <= start {
						continue
					}
					key := e.drum + ":" + strconv.Itoa(int((max(start, e.tick)-start)/grid))
					if previous, ok := winner[key]; ok {
						report.MergedHits++
						if e.vel <= lane.events[previous].vel {
							continue
						}
					}
					winner[key] = i
				}
			}
			for i, e := range lane.events {
				eventEnd := e.tick + e.dur
				if e.tick >= start+seq.TicksPerBar || eventEnd <= start {
					continue
				}
				from := max(start, e.tick)
				to := min(start+seq.TicksPerBar, eventEnd)
				index := int((from - start) / grid)
				if lane.drums && winner[e.drum+":"+strconv.Itoa(index)] != i {
					continue
				}
				if from != e.tick {
					report.SplitNotes++
				}
				if lane.drums {
					velocity := int((int(e.vel) + 7) / 14)
					velocity = max(1, min(9, velocity))
					hit := "x" + strconv.Itoa(velocity)
					if e.vel == 127 {
						hit = "X"
					} else if e.vel == 100 {
						hit = "x"
					} else if int(e.vel) != velocity*14 {
						report.QuantizedVelocities++
					}
					if rows[e.drum] == nil {
						rows[e.drum] = make([]string, len(cells))
						for c := range rows[e.drum] {
							rows[e.drum][c] = "."
						}
					}
					rows[e.drum][index] = hit
					if grid*gate/100 != to-from {
						report.QuantizedDurations++
					}
					continue
				}
				cells[index] = importStep(e.pitches)
				velocities[index] = strconv.Itoa(int(e.vel))
				width := max(int64(1), (to-from+grid/2)/grid)
				for offset := int64(1); offset < width && index+int(offset) < len(cells); offset++ {
					cells[index+int(offset)] = "-"
					velocities[index+int(offset)] = strconv.Itoa(int(e.vel))
				}
				duration := grid * gate / 100
				if width > 1 {
					duration = width * grid
				}
				if duration != to-from {
					report.QuantizedDurations++
				}
			}
			var body string
			if lane.drums {
				var lines []string
				for _, name := range drum.Names {
					if rows[name] != nil {
						lines = append(lines, name+": "+strings.Join(rows[name], " "))
					}
				}
				if len(lines) == 0 {
					// A bar with no hits still needs one row.
					lines = append(lines, lane.events[0].drum+": "+strings.Join(cells, " "))
				}
				body = strings.Join(lines, "\n ")
			} else {
				body = strings.Join(cells, " ") + "\n velocity: " + strings.Join(velocities, " ")
			}
			id, ok := pool[body]
			if !ok {
				if len(pool) == 16 {
					return nil, patternLimitError{source: lane.source}
				}
				id = fmt.Sprintf("%s-bar-%d", trackID, len(pool)+1)
				pool[body] = id
				fmt.Fprintf(&output, "pattern %s", id)
				if lane.drums {
					output.WriteString(" drums")
				}
				fmt.Fprintf(&output, " {\n step = %d/3840\n gate = %d%%\n %s\n}\n", grid, gate, body)
			}
			bindings[bar] = append(bindings[bar], trackID+" = "+id)
		}
	}
	for bar, assignments := range bindings {
		fmt.Fprintf(&output, "scene midi-bar-%d { %s }\n", bar+1, strings.Join(assignments, " "))
	}
	output.WriteString("song { ")
	for bar := range bindings {
		fmt.Fprintf(&output, "midi-bar-%d ", bar+1)
	}
	output.WriteString("}\n")
	source := []byte(output.String())
	score, ds := notation.Parse(source)
	if importErrors(ds) {
		return fail(fmt.Sprint(ds))
	}
	if p, ds := project.FromScore(score); p == nil {
		return fail(fmt.Sprint(ds))
	}
	document, err := notation.ParseDocument(source)
	if err != nil {
		return nil, err
	}
	return notation.Format(document)
}

// importTrackNames names each lane after its MIDI track: the track name as a
// score identifier, with -2, -3 for the track's further lanes. A track whose
// name the score cannot use, such as a keyword, keeps the midi-N name of its
// lane; the other tracks keep their names.
func importTrackNames(file File, lanes []importLane) []string {
	names := make([]string, len(lanes))
	used := map[string]bool{}
	for i, lane := range lanes {
		base := importIdentifier(file.Tracks[lane.source].Name)
		if base != "" && !importNameParses(base) {
			base = ""
		}
		if base == "" {
			base = fmt.Sprintf("midi-%d", i+1)
		}
		name := base
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		used[name] = true
		names[i] = name
	}
	return names
}

// importNameParses reports whether a track ID works where the import writes
// it: as a track, in its pattern IDs and in a scene binding.
func importNameParses(name string) bool {
	source := "cicada 2\ntrack " + name + " piano {}\npattern " + name + "-bar-16 { c4 }\nscene s { " + name + " = " + name + "-bar-16 }\nsong { s }\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil || importErrors(ds) {
		return false
	}
	p, _ := project.FromScore(score)
	return p != nil
}

// importIdentifier turns a MIDI track name into a score identifier
// ([a-z_][a-z0-9_-]*), or returns "" when nothing usable is left.
func importIdentifier(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	// Leave room under the 64-byte identifier limit for "-bar-16" and a
	// "-N" suffix on the track's further lanes.
	id := b.String()
	if len(id) > 54 {
		id = id[:54]
	}
	id = strings.TrimRight(id, "-")
	if id == "" || id[0] >= '0' && id[0] <= '9' {
		return ""
	}
	return id
}

// importTitle is the name of a type-1 file's conductor track when it is plain
// printable ASCII, else "MIDI import".
func importTitle(file File) string {
	if file.Format != 1 || len(file.Tracks) == 0 || len(file.Tracks[0].Notes) != 0 {
		return "MIDI import"
	}
	name := strings.TrimSpace(file.Tracks[0].Name)
	if name == "" {
		return "MIDI import"
	}
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return "MIDI import"
		}
	}
	return name
}

// importStep spells a step's pitches: one note, or a chord such as [c4 e4 g4].
func importStep(pitches []uint8) string {
	if len(pitches) == 1 {
		return importPitch(pitches[0])
	}
	parts := make([]string, len(pitches))
	for i, pitch := range pitches {
		parts[i] = importPitch(pitch)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func importPitch(note uint8) string {
	names := [12]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}
	return names[note%12] + strconv.Itoa(int(note)/12-1)
}

func importErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
