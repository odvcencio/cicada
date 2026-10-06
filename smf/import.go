package smf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

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
}

type importLane struct {
	notes []Note
	drum  string
}

// ImportSource imports fixed-tempo 4/4 SMF notes to a validated edition-2
// score. Overlapping melodic notes get independent piano tracks. Notes that
// cross a bar are split and retriggered; the report counts every timing edit.
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
	var notes []Note
	end := int64(0)
	grid := int64(960)
	for _, track := range file.Tracks {
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
			notes = append(notes, note)
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
		return notes[i].Note < notes[j].Note
	})
	var lanes []importLane
	for _, note := range notes {
		name := ""
		if note.Chan == 9 {
			for i, pitch := range drum.MIDINotes {
				if pitch == note.Note {
					name = drum.Names[i]
					break
				}
			}
			if name == "" {
				return fail("drum note has no built-in lane")
			}
		}
		selected := -1
		for i, lane := range lanes {
			if lane.drum != name {
				continue
			}
			last := lane.notes[len(lane.notes)-1]
			if last.Tick+last.Dur <= note.Tick {
				selected = i
				break
			}
		}
		if selected < 0 {
			if len(lanes) == 16 {
				return fail("polyphony needs more than 16 score tracks")
			}
			selected = len(lanes)
			lanes = append(lanes, importLane{drum: name})
		}
		lanes[selected].notes = append(lanes[selected].notes, note)
	}
	report.Tracks = len(lanes)
	var output strings.Builder
	fmt.Fprintf(&output, "cicada 2\ntitle %q\ntempo %d.%03d\n", "MIDI import", bpm/1000, bpm%1000)
	bindings := make([][]string, report.Bars)
	for ti, lane := range lanes {
		trackID := fmt.Sprintf("midi-%d", ti+1)
		kind := "piano"
		if lane.drum != "" {
			kind = "drums"
		}
		fmt.Fprintf(&output, "track %s %s {}\n", trackID, kind)
		gate := int64(100)
		{
			for _, n := range lane.notes {
				if n.Dur < grid {
					gate = max(int64(10), min(int64(100), (n.Dur*100+grid/2)/grid))
					break
				}
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
			start := int64(bar) * seq.TicksPerBar
			for _, n := range lane.notes {
				noteEnd := n.Tick + n.Dur
				if n.Tick >= start+seq.TicksPerBar || noteEnd <= start {
					continue
				}
				from := max(start, n.Tick)
				to := min(start+seq.TicksPerBar, noteEnd)
				index := int((from - start) / grid)
				if from != n.Tick {
					report.SplitNotes++
				}
				if lane.drum != "" {
					velocity := int((int(n.Vel) + 7) / 14)
					velocity = max(1, min(9, velocity))
					hit := "x" + strconv.Itoa(velocity)
					if n.Vel == 127 {
						hit = "X"
					} else if n.Vel == 100 {
						hit = "x"
					} else if int(n.Vel) != velocity*14 {
						report.QuantizedVelocities++
					}
					cells[index] = hit
					if grid*gate/100 != to-from {
						report.QuantizedDurations++
					}
				} else {
					cells[index] = importPitch(n.Note)
					velocities[index] = strconv.Itoa(int(n.Vel))
					width := max(int64(1), (to-from+grid/2)/grid)
					for offset := int64(1); offset < width && index+int(offset) < len(cells); offset++ {
						cells[index+int(offset)] = "-"
						velocities[index+int(offset)] = strconv.Itoa(int(n.Vel))
					}
					duration := grid * gate / 100
					if width > 1 {
						duration = width * grid
					}
					if duration != to-from {
						report.QuantizedDurations++
					}
				}
			}
			body := strings.Join(cells, " ")
			if lane.drum != "" {
				body = lane.drum + ": " + body
			} else {
				body += "\n velocity: " + strings.Join(velocities, " ")
			}
			id, ok := pool[body]
			if !ok {
				if len(pool) == 16 {
					return fail("track needs more than 16 distinct bar patterns")
				}
				id = fmt.Sprintf("midi-%d-bar-%d", ti+1, len(pool)+1)
				pool[body] = id
				fmt.Fprintf(&output, "pattern %s", id)
				if lane.drum != "" {
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
		return nil, report, err
	}
	formatted, err := notation.Format(document)
	if err != nil {
		return nil, report, err
	}
	return formatted, report, nil
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
