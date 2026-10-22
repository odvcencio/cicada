package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

var chordShapes = []struct {
	id, label string
	intervals []int
}{
	{"major", "Major", []int{0, 4, 7}},
	{"minor", "Minor", []int{0, 3, 7}},
	{"sus2", "Sus 2", []int{0, 2, 7}},
	{"sus4", "Sus 4", []int{0, 5, 7}},
	{"diminished", "Diminished", []int{0, 3, 6}},
	{"augmented", "Augmented", []int{0, 4, 8}},
	{"seventh", "Dominant 7", []int{0, 4, 7, 10}},
	{"major7", "Major 7", []int{0, 4, 7, 11}},
	{"minor7", "Minor 7", []int{0, 3, 7, 10}},
}

func chordPitches(root, octave int, shape string, inversion int) ([]int, error) {
	for _, candidate := range chordShapes {
		if candidate.id != shape {
			continue
		}
		if root < 0 || root > 11 || octave < -1 || octave > 9 || inversion < 0 || inversion >= len(candidate.intervals) {
			return nil, fmt.Errorf("choose a root, octave, and inversion supported by this chord")
		}
		var pitches []int
		for _, interval := range candidate.intervals[inversion:] {
			pitches = append(pitches, (octave+1)*12+root+interval)
		}
		for _, interval := range candidate.intervals[:inversion] {
			pitches = append(pitches, (octave+1)*12+root+interval+12)
		}
		if pitches[len(pitches)-1] > 127 {
			return nil, fmt.Errorf("this voicing extends past MIDI 127; choose a lower octave")
		}
		return pitches, nil
	}
	return nil, fmt.Errorf("choose a chord type")
}

func stepPitches(note *project.Step) []int {
	if note == nil || note.Tie {
		return nil
	}
	if len(note.Notes) > 0 {
		return note.Notes
	}
	return []int{int(note.Note)}
}

func stepHasPitch(note *project.Step, pitch int) bool {
	if note == nil || note.Tie {
		return false
	}
	if len(note.Notes) == 0 {
		return int(note.Note) == pitch
	}
	return slices.Contains(note.Notes, pitch)
}

func (s *studioApp) chordEditor(ctx *server.Context, p project.Pattern, step int, revision string, key project.Key, form func(string, ...gosx.Node) gosx.Node) gosx.Node {
	if p.Kind != "notes" || p.Performance != nil {
		return gosx.Fragment()
	}
	shape := "major"
	if strings.Contains(key.Scale, "minor") {
		shape = "minor"
	}
	values := map[string]string{"chord_root": strconv.Itoa(int(key.Root)), "chord_octave": "3", "chord_shape": shape, "chord_inversion": "0", "hold": strconv.Itoa(min(4, int(p.Steps)-step+1))}
	var current []string
	if step <= len(p.Data) && p.Data[step-1] != nil && !p.Data[step-1].Tie {
		note := p.Data[step-1]
		pitches := slices.Clone(stepPitches(note))
		slices.Sort(pitches)
		for _, pitch := range pitches {
			current = append(current, noteName(pitch))
		}
		values["chord_root"], values["chord_octave"] = strconv.Itoa(pitches[0]%12), strconv.Itoa(pitches[0]/12-1)
		hold := 1
		for i := step; i < len(p.Data) && p.Data[i] != nil && p.Data[i].Tie; i++ {
			hold++
		}
		values["hold"] = strconv.Itoa(hold)
		// Recognize an existing voicing so changing its hold does not reset its
		// root or inversion. Unrecognized voicings remain visible as pitches.
		matched := false
		for _, candidate := range chordShapes {
			for inversion, interval := range candidate.intervals {
				root := pitches[0] - interval
				if root < 0 {
					continue
				}
				notes, err := chordPitches(root%12, root/12-1, candidate.id, inversion)
				if err == nil && slices.Equal(notes, pitches) {
					values["chord_root"], values["chord_octave"], values["chord_shape"], values["chord_inversion"] = strconv.Itoa(root%12), strconv.Itoa(root/12-1), candidate.id, strconv.Itoa(inversion)
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	}
	failed := false
	if state := action.States(ctx.Request)["pattern"]; !state.OK() && state.Value("revision") == revision && state.Value("action") == "chord" && state.Value("pattern") == p.ID && state.Value("step") == strconv.Itoa(step) {
		failed = true
		for name := range values {
			values[name] = state.Value(name)
		}
	}
	var roots, shapes, inversions []gosx.Node
	for i, name := range noteNames {
		roots = append(roots, gosx.El("option", gosx.Attrs(gosx.Attr("value", strconv.Itoa(i)), gosx.Attr("selected", values["chord_root"] == strconv.Itoa(i))), gosx.Text(name)))
	}
	for _, shape := range chordShapes {
		shapes = append(shapes, gosx.El("option", gosx.Attrs(gosx.Attr("value", shape.id), gosx.Attr("selected", values["chord_shape"] == shape.id)), gosx.Text(shape.label)))
	}
	for i, label := range []string{"Root", "First", "Second", "Third (7th)"} {
		inversions = append(inversions, gosx.El("option", gosx.Attrs(gosx.Attr("value", strconv.Itoa(i)), gosx.Attr("selected", values["chord_inversion"] == strconv.Itoa(i))), gosx.Text(label)))
	}
	controls := form("pattern", hidden("action", "chord"), hidden("pattern", p.ID), hidden("step", strconv.Itoa(step)), field("Root", gosx.El("select", gosx.Attrs(gosx.Attr("name", "chord_root")), gosx.Fragment(roots...))), field("Chord type", gosx.El("select", gosx.Attrs(gosx.Attr("name", "chord_shape")), gosx.Fragment(shapes...))), field("Root octave", numberInput("chord_octave", values["chord_octave"], "-1", "9")), field("Voicing", gosx.El("select", gosx.Attrs(gosx.Attr("name", "chord_inversion")), gosx.Fragment(inversions...))), field("Hold (steps)", numberInput("hold", values["hold"], "1", strconv.Itoa(int(p.Steps)-step+1))), submit("", "", "Place chord"), gridFormStatus(ctx, p.ID, "chord"))
	var existing gosx.Node = gosx.Fragment()
	if len(current) > 0 {
		existing = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Current: "+strings.Join(current, " · ")))
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management"), gosx.Attr("data-gosx-key", "chord-editor"), gosx.Attr("open", failed || len(current) > 1)), gosx.El("summary", gosx.Text("Build a chord")), existing, controls, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Replaces the chosen span with a chord and holds. Shortening clears its old trailing holds. One Undo restores the prior notes.")))
}
