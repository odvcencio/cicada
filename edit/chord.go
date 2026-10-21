package edit

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/notation"
)

// SetChord places a voicing and its hold steps as one reversible edit. Hold
// includes the onset. Shortening an existing hold clears its remaining ties.
type SetChord struct {
	Entity EntityID `json:"entity"`
	Notes  []int    `json:"notes"`
	Hold   int      `json:"hold"`
	Shared string   `json:"shared,omitempty"`
}

func (SetChord) Kind() string { return "setchord" }

func init() {
	Register("setchord", func() Intent { return &SetChord{} })
	Handle("setchord", setChord)
}

func setChord(ctx *Context, intent Intent) error {
	in := intent.(*SetChord)
	id, lane, index, err := stepTarget(in.Entity)
	if err != nil {
		return err
	}
	if lane != "" || index < 0 || index >= 64 || in.Hold < 1 || in.Hold > 64 || len(in.Notes) < 2 || len(in.Notes) > 4 {
		return fmt.Errorf("choose a grid step, 2–4 distinct MIDI pitches, and a hold of 1–64 steps in a notes pattern")
	}
	seen := map[int]bool{}
	for _, pitch := range in.Notes {
		if pitch < 0 || pitch > 127 || seen[pitch] {
			return fmt.Errorf("choose 2–4 distinct MIDI pitches from 0 to 127")
		}
		seen[pitch] = true
	}
	if err := patternPreflight(ctx); err != nil {
		return err
	}
	if _, _, err := declaration(ctx.Source, patternTypes, id); err != nil {
		return err
	}
	initial, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	last := index + in.Hold - 1
	found := false
	for _, pattern := range initial.Patterns {
		if pattern.ID != id {
			continue
		}
		if pattern.Kind != "notes" || pattern.Performance != nil || index < 0 || last >= len(pattern.Data) {
			return fmt.Errorf("the chord and its hold must fit inside a notes pattern")
		}
		found = true
		// Clearing the old tail is a real edit too. Resolve its shared uses
		// before taking any offsets, including ties beyond the new hold.
		for last+1 < len(pattern.Data) && pattern.Data[last+1] != nil && pattern.Data[last+1].Tie {
			last++
		}
		break
	}
	if !found {
		return fmt.Errorf("unknown pattern %q", id)
	}
	local, err := resolvePatternRange(ctx, id, "", index, last, in.Shared)
	if err != nil {
		return err
	}
	working := sourceContext(ctx, local)
	score, ds := working.Parse()
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("pattern must validate before placing a chord")
	}
	plan, err := working.CurrentPlan()
	if err != nil {
		return err
	}
	var data []*Step
	for _, pattern := range plan.Patterns {
		if pattern.ID == id {
			data = pattern.Data
		}
	}
	for _, pattern := range score.Patterns {
		if pattern.Name != id {
			continue
		}
		if pattern.Kind != "notes" || pattern.Performance != nil || index < 0 || index+in.Hold > len(pattern.Steps) || len(data) != len(pattern.Steps) {
			return fmt.Errorf("the chord and its hold must fit inside a notes pattern")
		}
		token := pattern.Steps[index]
		if old := data[index]; old != nil && !old.Tie && (old.Slide || old.Ratchet > 1) {
			return fmt.Errorf("remove slide or ratchet before replacing this note with a chord")
		}
		pitches := make([]int, len(in.Notes))
		for i, pitch := range in.Notes {
			pitches[i] = pitch - token.Transpose
			if pitches[i] < 0 || pitches[i] > 127 {
				return fmt.Errorf("chord pitches cannot be written through phrase transpose %+d", token.Transpose)
			}
		}
		suffix := ""
		if old := data[index]; old != nil && !old.Tie {
			suffix = noteSuffix(token.Text)
			if len(token.ChordPitches) > 0 {
				suffix = token.ChordModifiers
			}
		}
		text, err := chordSource(token, pitches, suffix, data[index])
		if err != nil {
			return err
		}
		var patches []Span
		patch := func(i int, text string) error {
			token := pattern.Steps[i]
			at, err := Offset(local, ctx.Options.Path, token.Position)
			if err != nil {
				return err
			}
			if at+len(token.Text) > len(local) || string(local[at:at+len(token.Text)]) != token.Text {
				return fmt.Errorf("step no longer matches its source")
			}
			patches = append(patches, Span{at, at + len(token.Text), text, ctx.Options.Path})
			return nil
		}
		if err := patch(index, text); err != nil {
			return err
		}
		for i := index + 1; i < index+in.Hold; i++ {
			if err := patch(i, "-"); err != nil {
				return err
			}
		}
		for i := index + in.Hold; i < len(data) && data[i] != nil && data[i].Tie; i++ {
			if err := patch(i, "."); err != nil {
				return err
			}
		}
		updated, err := PatchSpans(local, ctx.Options.Path, patches)
		if err != nil {
			return err
		}
		ctx.Source, ctx.plan = updated, nil
		ctx.SetLabel(fmt.Sprintf("Pattern · %s step %d chord · %d steps", id, index+1, in.Hold))
		return nil
	}
	return fmt.Errorf("unknown pattern %q", id)
}

// Preserve authored pitch spans and annotations when a chord keeps its size
// and dynamics. Reshaping retains its comments alongside the replacement.
func chordSource(token notation.StepToken, pitches []int, suffix string, original *Step) (string, error) {
	var names []string
	for i, pitch := range pitches {
		name := sourcePitch(pitch)
		if original != nil && i < len(original.Notes) && i < len(token.ChordPitches) && pitch == original.Notes[i]-token.Transpose {
			name = token.ChordPitches[i].Text
		}
		names = append(names, name)
	}
	if len(token.ChordPitches) == len(pitches) && suffix == token.ChordModifiers {
		var patches []Span
		for i, pitch := range token.ChordPitches {
			if names[i] != pitch.Text {
				patches = append(patches, Span{pitch.Start, pitch.End, names[i], token.Position.File})
			}
		}
		if len(patches) == 0 {
			return token.Text, nil
		}
		updated, err := PatchSpans([]byte(token.Text), token.Position.File, patches)
		return string(updated), err
	}
	prefix := ""
	if len(token.ChordComments) > 0 {
		newline := "\n"
		if strings.Contains(token.Text, "\r\n") {
			newline = "\r\n"
		}
		prefix = strings.Join(token.ChordComments, newline) + newline
	}
	return prefix + "[" + strings.Join(names, " ") + "]" + suffix, nil
}

func shiftedStepSource(token notation.StepToken, note *Step, amount int) (string, error) {
	shift := func(pitch int) (int, error) {
		if pitch+amount < 0 || pitch+amount > 127 || pitch+amount-token.Transpose < 0 || pitch+amount-token.Transpose > 127 {
			return 0, fmt.Errorf("transposed note would leave MIDI range 0–127")
		}
		return pitch + amount - token.Transpose, nil
	}
	if len(note.Notes) == 0 {
		pitch, err := shift(int(note.Note))
		if err != nil {
			return "", err
		}
		return sourcePitch(pitch) + noteSuffix(token.Text), nil
	}
	pitches := make([]int, len(note.Notes))
	for i, pitch := range note.Notes {
		var err error
		if pitches[i], err = shift(pitch); err != nil {
			return "", err
		}
	}
	return chordSource(token, pitches, token.ChordModifiers, nil)
}
