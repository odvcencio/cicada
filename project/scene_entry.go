package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// sceneEntryWarnings reports a scene that starts a pattern inside one of the
// pattern's held notes. Scene patterns keep the song's phase, so a pattern
// bound after bar 1 enters at the step the song has reached. When that step
// continues a tied note, or falls inside a note that is still held, the note
// began before the scene and never sounds: the track is silent until the
// pattern's next note. That is the documented phase rule, but it silences a
// part without saying so, so check reports it as CICADA-SCENE-ENTRY.
func sceneEntryWarnings(p *Project, score *notation.Score) []notation.Diagnostic {
	if p == nil || score == nil || p.Arrange != nil || len(p.Song) == 0 {
		return nil
	}
	patterns := make(map[string]*Pattern, len(p.Patterns))
	for i := range p.Patterns {
		patterns[p.Patterns[i].ID] = &p.Patterns[i]
	}
	scenes := make(map[string]*Scene, len(p.Scenes))
	for i := range p.Scenes {
		scenes[p.Scenes[i].ID] = &p.Scenes[i]
	}
	positions := map[[2]string]notation.Position{}
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			positions[[2]string{scene.Name, binding.Track}] = binding.Position
		}
	}
	// active holds each track's pattern; a chain is not a pattern, so a scene
	// that binds one takes the track over at the song's phase.
	active := make([]string, len(p.Tracks))
	reported := map[[2]string]bool{}
	var out []notation.Diagnostic
	var tick int64
	for _, entry := range p.Song {
		scene := scenes[entry.Scene]
		for ti, track := range p.Tracks {
			if scene == nil {
				break
			}
			name, ok := scene.Bindings[track.ID]
			if !ok || name == active[ti] {
				continue
			}
			active[ti] = name
			key := [2]string{scene.ID, track.ID}
			pattern := patterns[name]
			if name == "off" || pattern == nil || reported[key] {
				continue
			}
			entered, ok := heldEntry(pattern, tick)
			if !ok {
				continue
			}
			reported[key] = true
			out = append(out, notation.Diagnostic{Code: "CICADA-SCENE-ENTRY", Severity: "warning", Position: positions[key], Message: entered.message(pattern.ID, track.ID, tick)})
		}
		tick += int64(entry.Bars) * seq.TicksPerBar
	}
	return out
}

type sceneEntry struct {
	step, onset, steps int
	next               int64 // tick of the pattern's next note, or -1
}

// heldEntry finds the note a pattern bound at tick would be holding there.
// ok is false when the entry starts a note, a rest, or a note already over.
func heldEntry(p *Pattern, tick int64) (sceneEntry, bool) {
	if p.Performance != nil || p.Kind == "drums" || p.Steps == 0 || len(p.Data) != int(p.Steps) {
		return sceneEntry{}, false
	}
	grid := int64(p.StepTicks)
	if grid == 0 {
		grid = seq.TicksPerStep
	}
	n := int64(p.Steps)
	at := func(step int64) *Step { return p.Data[step%n] }
	cell := tick / grid
	current := at(cell)
	if current == nil || tick%grid == 0 && !current.Tie {
		return sceneEntry{}, false
	}
	// Walk back over the ties to the step that starts the note.
	onset := cell
	for onset >= 0 && at(onset) != nil && at(onset).Tie && cell-onset < n {
		onset--
	}
	if onset < 0 || at(onset) == nil || at(onset).Tie {
		return sceneEntry{}, false
	}
	// A tied note holds to the end of its last tied step; an untied note
	// holds for the pattern's gate.
	last := cell
	for last+1-onset < n && at(last+1) != nil && at(last+1).Tie {
		last++
	}
	end := (last + 1) * grid
	if last == onset {
		end = onset*grid + max(30, grid*int64(p.GatePercent)/100)
	}
	if end <= tick {
		return sceneEntry{}, false
	}
	entry := sceneEntry{step: int(cell%n) + 1, onset: int(onset%n) + 1, steps: int(n), next: -1}
	for k := cell + 1; k <= cell+n; k++ {
		if s := at(k); s != nil && !s.Tie {
			entry.next = k * grid
			break
		}
	}
	return entry, true
}

func (e sceneEntry) message(pattern, track string, tick int64) string {
	where := fmt.Sprintf("pattern %s on track %s enters at bar %d on step %d of %d", pattern, track, tick/seq.TicksPerBar+1, e.step, e.steps)
	held := fmt.Sprintf("inside the note that step %d starts", e.onset)
	if e.step == e.onset {
		held = "after the start of its note"
	}
	until := "the pattern has no later note"
	if e.next >= 0 {
		until = "the track is silent until " + songPosition(e.next)
	}
	return fmt.Sprintf("%s, %s: scene patterns keep the song's phase, so that note began before the scene and %s; place the pattern with arrange to start it from its first step", where, held, until)
}

// songPosition writes a song tick as @bar.beat.sixteenth, the form arrange
// placements use, and adds the remaining ticks (960 to a beat) when the tick
// falls between sixteenths.
func songPosition(tick int64) string {
	const sixteenth = seq.TicksPerBar / 16
	at := fmt.Sprintf("@%d.%d.%d", tick/seq.TicksPerBar+1, tick%seq.TicksPerBar/(4*sixteenth)+1, tick%(4*sixteenth)/sixteenth+1)
	if rest := tick % sixteenth; rest != 0 {
		at += fmt.Sprintf(" plus %d ticks", rest)
	}
	return at
}
